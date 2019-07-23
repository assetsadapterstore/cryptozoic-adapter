/*
 * Copyright 2018 The openwallet Authors
 * This file is part of the openwallet library.
 *
 * The openwallet library is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The openwallet library is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Lesser General Public License for more details.
 */
package cryptozoic

import (
	"github.com/blocktree/ethereum-adapter/ethereum"
	"github.com/blocktree/openwallet/common"
	"math/big"
	"strings"
	"time"

	"github.com/asdine/storm"
	"github.com/blocktree/openwallet/openwallet"

	//	"fmt"
	"errors"

	//	"golang.org/x/text/currency"

	"fmt"
)

const (
	//BLOCK_CHAIN_BUCKET = "blockchain" //区块链数据集合
	//periodOfTask      = 5 * time.Second //定时任务执行隔间
	MAX_EXTRACTING_SIZE = 15 //并发的扫描线程数

	BLOCK_HASH_KEY   = "BlockHash"
	BLOCK_HEIGHT_KEY = "BlockHeight"
)

type VCCBlockScanner struct {
	*openwallet.BlockScannerBase
	CurrentBlockHeight   uint64         //当前区块高度
	extractingCH         chan struct{}  //扫描工作令牌
	wm                   *WalletManager //钱包管理者
	IsScanMemPool        bool           //是否扫描交易池
	RescanLastBlockCount uint64         //重扫上N个区块数量
}

//ExtractResult 扫描完成的提取结果
type ExtractResult struct {
	extractData map[string][]*openwallet.TxExtractData

	//Recharges   []*openwallet.Recharge
	TxID        string
	BlockHeight uint64
	Success     bool
}

//SaveResult 保存结果
type SaveResult struct {
	TxID        string
	BlockHeight uint64
	Success     bool
}

//NewVCCBlockScanner 创建区块链扫描器
func NewVCCBlockScanner(wm *WalletManager) *VCCBlockScanner {
	bs := VCCBlockScanner{
		BlockScannerBase: openwallet.NewBlockScannerBase(),
	}

	bs.extractingCH = make(chan struct{}, MAX_EXTRACTING_SIZE)
	bs.wm = wm
	bs.IsScanMemPool = false
	bs.RescanLastBlockCount = 0

	//设置扫描任务
	bs.SetTask(bs.ScanBlockTask)

	return &bs
}

//SetRescanBlockHeight 重置区块链扫描高度
func (this *VCCBlockScanner) SetRescanBlockHeight(height uint64) error {
	height = height - 1
	if height < 0 {
		return errors.New("block height to rescan must greater than 0.")
	}

	block, err := this.wm.WalletClient.EthGetBlockSpecByBlockNum(height, false)
	if err != nil {
		this.wm.Log.Errorf("get block spec by block number[%v] failed, err=%v", height, err)
		return err
	}

	err = this.wm.SaveLocalBlockScanned(height, block.BlockHash)
	if err != nil {
		this.wm.Log.Errorf("save local block scanned failed, err=%v", err)
		return err
	}

	return nil
}

func (this *VCCBlockScanner) newBlockNotify(block *ethereum.EthBlock, isFork bool) {
	header := block.CreateOpenWalletBlockHeader()
	header.Fork = isFork
	header.Symbol = this.wm.Config.Symbol
	this.NewBlockNotify(header)
}

func (this *VCCBlockScanner) ScanBlock(height uint64) error {
	curBlock, err := this.wm.WalletClient.EthGetBlockSpecByBlockNum(height, true)
	if err != nil {
		this.wm.Log.Errorf("EthGetBlockSpecByBlockNum failed, err = %v", err)
		return err
	}

	err = this.BatchExtractTransaction(curBlock.Transactions)
	if err != nil {
		this.wm.Log.Errorf("BatchExtractTransaction failed, err = %v", err)
		return err
	}

	this.newBlockNotify(curBlock, false)

	return nil
}

func (this *VCCBlockScanner) ScanTxMemPool() error {
	this.wm.Log.Infof("block scanner start to scan mempool.")

	txs, err := this.GetTxPoolPendingTxs()
	if err != nil {
		this.wm.Log.Errorf("get txpool pending txs failed, err=%v", err)
		return err
	}

	err = this.BatchExtractTransaction(txs)
	if err != nil {
		this.wm.Log.Errorf("batch extract transactions failed, err=%v", err)
		return err
	}
	return nil
}

func (this *VCCBlockScanner) RescanFailedTransactions() error {
	unscannedTxs, err := this.wm.GetAllUnscannedTransactions()
	if err != nil {
		this.wm.Log.Errorf("GetAllUnscannedTransactions failed. err=%v", err)
		return err
	}

	txs, err := this.wm.RecoverUnscannedTransactions(unscannedTxs)
	if err != nil {
		this.wm.Log.Errorf("recover transactions from unscanned result failed. err=%v", err)
		return err
	}

	err = this.BatchExtractTransaction(txs)
	if err != nil {
		this.wm.Log.Errorf("batch extract transactions failed, err=%v", err)
		return err
	}

	err = this.wm.DeleteUnscannedTransactions(unscannedTxs)
	if err != nil {
		this.wm.Log.Errorf("batch extract transactions failed, err=%v", err)
		return err
	}
	return nil
}

func (this *VCCBlockScanner) ScanBlockTask() {

	//获取本地区块高度
	blockHeader, err := this.GetScannedBlockHeader()
	if err != nil {
		this.wm.Log.Errorf("block scanner can not get new block height; unexpected error: %v", err)
		return
	}

	curBlockHeight := blockHeader.Height
	curBlockHash := blockHeader.Hash
	var previousHeight uint64 = 0
	for {

		if !this.Scanning {
			//区块扫描器已暂停，马上结束本次任务
			return
		}

		maxBlockHeight, err := this.wm.WalletClient.EthGetBlockNumber()
		if err != nil {
			this.wm.Log.Errorf("get max height of eth failed, err=%v", err)
			break
		}

		this.wm.Log.Info("current block height:", curBlockHeight, " maxBlockHeight:", maxBlockHeight)
		if curBlockHeight == maxBlockHeight {
			this.wm.Log.Infof("block scanner has done with scan. current height:%v", maxBlockHeight)
			break
		}

		//扫描下一个区块
		curBlockHeight += 1
		this.wm.Log.Infof("block scanner try to scan block No.%v", curBlockHeight)

		curBlock, err := this.wm.WalletClient.EthGetBlockSpecByBlockNum(curBlockHeight, true)
		if err != nil {
			this.wm.Log.Errorf("EthGetBlockSpecByBlockNum failed, err = %v", err)
			break
		}

		isFork := false

		if curBlock.PreviousHash != curBlockHash {
			previousHeight = curBlockHeight - 1
			this.wm.Log.Infof("block has been fork on height: %v.", curBlockHeight)
			this.wm.Log.Infof("block height: %v local hash = %v ", previousHeight, curBlockHash)
			this.wm.Log.Infof("block height: %v mainnet hash = %v ", previousHeight, curBlock.PreviousHash)

			this.wm.Log.Infof("delete recharge records on block height: %v.", previousHeight)

			//本地数据库并不存储交易
			//err = this.DeleteTransactionsByHeight(previousHeight)
			//if err != nil {
			//	this.wm.Log.Errorf("DeleteTransactionsByHeight failed, height=%v, err=%v", "0x"+strconv.FormatUint(previousHeight, 16), err)
			//	break
			//}

			//查询本地分叉的区块
			forkBlock, _ := this.wm.RecoverBlockHeader(previousHeight)

			err = this.wm.DeleteUnscannedTransactionByHeight(previousHeight)
			if err != nil {
				this.wm.Log.Errorf("DeleteUnscannedTransaction failed, height=%v, err=%v", previousHeight, err)
				break
			}

			curBlockHeight = previousHeight - 1 //倒退2个区块重新扫描

			curBlock, err = this.wm.RecoverBlockHeader(curBlockHeight)
			if err != nil && err != storm.ErrNotFound {
				this.wm.Log.Errorf("RecoverBlockHeader failed, block number=%v, err=%v", curBlockHeight, err)
				break
			} else if err == storm.ErrNotFound {
				curBlock, err = this.wm.WalletClient.EthGetBlockSpecByBlockNum(curBlockHeight, false)
				if err != nil {
					this.wm.Log.Errorf("EthGetBlockSpecByBlockNum  failed, block number=%v, err=%v", curBlockHeight, err)
					break
				}
			}
			curBlockHash = curBlock.BlockHash
			this.wm.Log.Infof("rescan block on height:%v, hash:%v.", curBlockHeight, curBlockHash)

			err = this.wm.SaveLocalBlockScanned(curBlock.BlockHeight, curBlock.BlockHash)
			if err != nil {
				this.wm.Log.Errorf("save local block unscaned failed, err=%v", err)
				break
			}

			isFork = true

			if forkBlock != nil {

				//通知分叉区块给观测者，异步处理
				this.newBlockNotify(forkBlock, isFork)
			}

		} else {
			err = this.BatchExtractTransaction(curBlock.Transactions)
			if err != nil {
				this.wm.Log.Errorf("block scanner can not extractRechargeRecords; unexpected error: %v", err)
				break
			}

			err = this.wm.SaveBlockHeader2(curBlock)
			if err != nil {
				this.wm.Log.Errorf("SaveBlockHeader2 failed")
			}

			isFork = false

			this.newBlockNotify(curBlock, isFork)
		}

		curBlockHeight = curBlock.BlockHeight
		curBlockHash = curBlock.BlockHash

	}

	if this.IsScanMemPool {
		this.ScanTxMemPool()
	}

	this.RescanFailedTransactions()
}

//newExtractDataNotify 发送通知
func (this *VCCBlockScanner) newExtractDataNotify(height uint64, tx *ethereum.BlockTransaction, extractDataList map[string][]*openwallet.TxExtractData) error {

	for o, _ := range this.Observers {
		for key, extractData := range extractDataList {
			for _, data := range extractData {
				this.wm.Log.Debugf("before notify, data.tx.Amount:%v", data.Transaction.Amount)
				err := o.BlockExtractDataNotify(key, data)
				if err != nil {
					//记录未扫区块
					//unscanRecord := NewUnscanRecord(height, "", "ExtractData Notify failed.")
					//err = this.SaveUnscanRecord(unscanRecord)
					reason := fmt.Sprintf("BlockExtractDataNotify account[%v] failed, err = %v", key, err)
					this.wm.Log.Errorf(reason)
					err = this.wm.SaveUnscannedTransaction(tx, reason)
					if err != nil {
						this.wm.Log.Errorf("block height: %d, save unscan record failed. unexpected error: %v", height, err.Error())
						return err
					}
				}
				this.wm.Log.Debugf("data.tx.Amount:%v", data.Transaction.Amount)
			}
		}
	}

	return nil
}

//BatchExtractTransaction 批量提取交易单
//bitcoin 1M的区块链可以容纳3000笔交易，批量多线程处理，速度更快
func (this *VCCBlockScanner) BatchExtractTransaction(txs []ethereum.BlockTransaction) error {
	for i := range txs {
		txs[i].FilterFunc = this.ScanAddressFunc
		extractResult, err := this.TransactionScanning(&txs[i])
		if err != nil {
			this.wm.Log.Errorf("transaction  failed, err=%v", err)
			return err
		}

		if extractResult.extractData != nil {
			err := this.newExtractDataNotify(txs[i].BlockHeight, &txs[i], extractResult.extractData)
			if err != nil {
				this.wm.Log.Errorf("newExtractDataNotify failed, err=%v", err)
				return err
			}
		}
	}
	return nil
}

func (this *VCCBlockScanner) GetTxPoolPendingTxs() ([]ethereum.BlockTransaction, error) {
	txpoolContent, err := this.wm.WalletClient.EthGetTxPoolContent()
	if err != nil {
		this.wm.Log.Errorf("get txpool content failed, err=%v", err)
		return nil, err
	}

	var txs []ethereum.BlockTransaction
	for from, txsets := range txpoolContent.Pending {
		if _, ok := this.ScanAddressFunc(strings.ToLower(from)); ok {
			for nonce, _ := range txsets {
				txs = append(txs, txsets[nonce])
			}
		} else {
			for nonce, _ := range txsets {
				if _, ok2 := this.ScanAddressFunc(strings.ToLower(txsets[nonce].To)); ok2 {
					txs = append(txs, txsets[nonce])
				}

			}
		}
	}
	return txs, nil
}

func (this *WalletManager) GetErc20TokenEvent(transactionID string) (map[string][]*ethereum.TransferEvent, error) {
	receipt, err := this.WalletClient.EthGetTransactionReceipt(transactionID)
	if err != nil {
		this.Log.Errorf("get transaction receipt failed, err=%v", err)
		return nil, err
	}

	transEvent := receipt.ParseTransferEvent()
	if transEvent == nil {
		return nil, nil
	}

	return transEvent, nil
}

func (this *VCCBlockScanner) UpdateTxByReceipt(tx *ethereum.BlockTransaction) (map[string][]*ethereum.TransferEvent, error) {
	//过滤掉未打包交易
	if tx.BlockHeight == 0 || tx.BlockHash == "" {
		return nil, nil
	}

	receipt, err := this.wm.WalletClient.EthGetTransactionReceipt(tx.Hash)
	if err != nil {
		this.wm.Log.Errorf("get transaction receipt failed, err=%v", err)
		return nil, err
	}

	tx.Gas = receipt.GasUsed
	tx.Status, err = ethereum.ConvertToUint64(receipt.Status, 16)
	if err != nil {
		return nil, err
	}
	// transEvent := receipt.ParseTransferEvent()
	// if transEvent == nil {
	// 	return nil, nil
	// }
	return receipt.ParseTransferEvent(), nil
}

func (this *VCCBlockScanner) MakeToExtractData(tx *ethereum.BlockTransaction, tokenEvent *ethereum.TransferEvent) (string, []*openwallet.TxExtractData, error) {
	if tokenEvent == nil {
		return this.MakeSimpleToExtractData(tx)
	}
	return this.MakeTokenToExtractData(tx, tokenEvent)
}

func (this *VCCBlockScanner) MakeSimpleToExtractData(tx *ethereum.BlockTransaction) (string, []*openwallet.TxExtractData, error) {
	var sourceKey string
	var exist bool
	var extractDataList []*openwallet.TxExtractData
	if sourceKey, exist = tx.FilterFunc(tx.To); !exist { //this.GetSourceKeyByAddress(tx.To)
		return "", extractDataList, nil
	}

	feeprice, err := tx.GetTxFeeEthString()
	if err != nil {
		this.wm.Log.Errorf("calc tx fee in eth failed, err=%v", err)
		return "", extractDataList, err
	}

	amountVal, err := tx.GetAmountEthString()
	if err != nil {
		this.wm.Log.Errorf("calc amount to eth decimal failed, err=%v", err)
		return "", extractDataList, err
	}

	nowUnix := time.Now().Unix()

	balanceTxOut := openwallet.TxOutPut{
		Recharge: openwallet.Recharge{
			Sid:      openwallet.GenTxOutPutSID(tx.Hash, this.wm.Symbol(), "", 0), //base64.StdEncoding.EncodeToString(crypto.SHA1([]byte(fmt.Sprintf("input_%s_%d_%s", tx.Hash, 0, tx.To)))),
			CreateAt: nowUnix,
			TxID:     tx.Hash,
			Address:  tx.To,
			Coin: openwallet.Coin{
				Symbol:     this.wm.Symbol(),
				IsContract: false,
			},
			Amount:      amountVal,
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxType:      0,
		},
	}

	from := []string{
		tx.From + ":" + amountVal,
	}
	to := []string{
		tx.To + ":" + amountVal,
	}

	transx := &openwallet.Transaction{
		WxID:        openwallet.GenTransactionWxID2(tx.Hash, this.wm.Symbol(), ""),
		TxID:        tx.Hash,
		From:        from,
		To:          to,
		Decimal:     18,
		BlockHash:   tx.BlockHash,
		BlockHeight: tx.BlockHeight,
		Fees:        feeprice,
		Coin: openwallet.Coin{
			Symbol:     this.wm.Symbol(),
			IsContract: false,
		},
		SubmitTime:  nowUnix,
		ConfirmTime: nowUnix,
		Status:      common.NewString(tx.Status).String(),
		TxType:      0,
	}

	txExtractData := &openwallet.TxExtractData{}
	txExtractData.TxOutputs = append(txExtractData.TxOutputs, &balanceTxOut)
	txExtractData.Transaction = transx

	extractDataList = append(extractDataList, txExtractData)

	return sourceKey, extractDataList, nil
}

func (this *VCCBlockScanner) GetBalanceByAddress(address ...string) ([]*openwallet.Balance, error) {
	type addressBalance struct {
		Address string
		Index   uint64
		Balance *openwallet.Balance
	}

	threadControl := make(chan int, 20)
	defer close(threadControl)
	resultChan := make(chan *addressBalance, 1024)
	defer close(resultChan)
	done := make(chan int, 1)
	count := len(address)
	resultBalance := make([]*openwallet.Balance, count)
	resultSaveFailed := false
	//save result
	go func() {
		for i := 0; i < count; i++ {
			addr := <-resultChan
			if addr.Balance != nil {
				resultBalance[addr.Index] = addr.Balance
			} else {
				resultSaveFailed = true
			}
		}
		done <- 1
	}()

	query := func(addr *addressBalance) {
		threadControl <- 1
		defer func() {
			resultChan <- addr
			<-threadControl
		}()

		balanceConfirmed, err := this.wm.WalletClient.GetAddrBalance2(ethereum.AppendOxToAddress(addr.Address), "latest")
		if err != nil {
			this.wm.Log.Error("get address[", addr.Address, "] balance failed, err=", err)
			return
		}

		balanceAll, err := this.wm.WalletClient.GetAddrBalance2(ethereum.AppendOxToAddress(addr.Address), "pending")
		if err != nil {
			//this.wm.Log.Errorf("get address[%v] erc20 token balance failed, err=%v", address, err)
			//return
			balanceAll = balanceConfirmed
		}

		//		this.wm.Log.Debugf("got balanceAll of [%v] :%v", address, balanceAll)
		balanceUnconfirmed := big.NewInt(0)
		balanceUnconfirmed.Sub(balanceAll, balanceConfirmed)

		balance := &openwallet.Balance{
			Symbol:  this.wm.Symbol(),
			Address: addr.Address,
		}
		confirmed, err := ethereum.ConverWeiStringToEthDecimal(balanceConfirmed.String())
		if err != nil {
			this.wm.Log.Errorf("ethereum.ConverWeiStringToEthDecimal confirmed balance failed, err=%v", err)
			return
		}
		all, err := ethereum.ConverWeiStringToEthDecimal(balanceAll.String())
		if err != nil {
			this.wm.Log.Errorf("ethereum.ConverWeiStringToEthDecimal all balance failed, err=%v", err)
			return
		}

		unconfirmed, err := ethereum.ConverWeiStringToEthDecimal(balanceUnconfirmed.String())
		if err != nil {
			this.wm.Log.Errorf("ethereum.ConverWeiStringToEthDecimal unconfirmed balance failed, err=%v", err)
			return
		}

		balance.Balance = all.String()
		balance.UnconfirmBalance = unconfirmed.String()
		balance.ConfirmBalance = confirmed.String()
		addr.Balance = balance
	}

	for i, _ := range address {
		addrbl := &addressBalance{
			Address: address[i],
			Index:   uint64(i),
		}
		go query(addrbl)
	}

	<-done
	if resultSaveFailed {
		return nil, errors.New("get balance of addresses failed.")
	}
	return resultBalance, nil
}

func (this *VCCBlockScanner) MakeTokenToExtractData(tx *ethereum.BlockTransaction, tokenEvent *ethereum.TransferEvent) (string, []*openwallet.TxExtractData, error) {
	var sourceKey string
	var exist bool
	var extractDataList []*openwallet.TxExtractData
	if sourceKey, exist = tx.FilterFunc(tokenEvent.TokenTo); !exist { //this.GetSourceKeyByAddress(tokenEvent.TokenTo)
		return "", extractDataList, nil
	}
	// fee, err := tx.GetTxFeeEthString()
	// if err != nil {
	// 	this.wm.Log.Errorf("calc tx fee in eth failed, err=%v", err)
	// 	return "", extractDataList, err
	// }

	contractId := openwallet.GenContractID(this.wm.Symbol(), tx.To) //base64.StdEncoding.EncodeToString(crypto.SHA256([]byte(fmt.Sprintf("{%v}_{%v}", this.wm.Symbol(), tx.To))))
	nowUnix := time.Now().Unix()

	coin := openwallet.Coin{
		Symbol:     this.wm.Symbol(),
		IsContract: true,
		ContractID: contractId,
		Contract: openwallet.SmartContract{
			ContractID: contractId,
			Address:    tx.To,
			Symbol:     this.wm.Symbol(),
		},
	}

	tokenValue, err := ethereum.ConvertToBigInt(tokenEvent.Value, 16)
	if err != nil {
		this.wm.Log.Errorf("convert token value to big.int failed, err=%v", err)
		return "", extractDataList, err
	}

	tokenBalanceTxOutput := openwallet.TxOutPut{
		Recharge: openwallet.Recharge{
			Sid:         openwallet.GenTxOutPutSID(tx.Hash, this.wm.Symbol(), contractId, 0), //base64.StdEncoding.EncodeToString(crypto.SHA1([]byte(fmt.Sprintf("input_%s_%d_%s", tx.Hash, 0, tokenEvent.TokenTo)))),
			CreateAt:    nowUnix,
			TxID:        tx.Hash,
			Address:     tokenEvent.TokenTo,
			Coin:        coin,
			Amount:      tokenValue.String(),
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxType:      0,
		},
	}
	from := []string{
		tokenEvent.TokenFrom + ":" + tokenValue.String(),
	}
	to := []string{
		tokenEvent.TokenTo + ":" + tokenValue.String(),
	}

	tokentransx := &openwallet.Transaction{
		WxID: openwallet.GenTransactionWxID2(tx.Hash, this.wm.Symbol(), contractId),
		TxID: tx.Hash,
		From: from,
		To:   to,
		//		Decimal:     18,
		BlockHash:   tx.BlockHash,
		BlockHeight: tx.BlockHeight,
		Fees:        "0", //tx.GasPrice, //totalSpent.Sub(totalReceived).StringFixed(8),
		Coin:        coin,
		SubmitTime:  nowUnix,
		ConfirmTime: nowUnix,
		Status:      common.NewString(tx.Status).String(),
		TxType:      0,
	}

	tokenTransExtractData := &openwallet.TxExtractData{}
	tokenTransExtractData.Transaction = tokentransx
	tokenTransExtractData.TxOutputs = append(tokenTransExtractData.TxOutputs, &tokenBalanceTxOutput)
	extractDataList = append(extractDataList, tokenTransExtractData)

	return sourceKey, extractDataList, nil
}

func (this *VCCBlockScanner) MakeFromExtractData(tx *ethereum.BlockTransaction, tokenEvent *ethereum.TransferEvent) (string, []*openwallet.TxExtractData, error) {
	if tokenEvent == nil {
		return this.MakeSimpleTxFromExtractData(tx)
	}
	return this.MakeTokenTxFromExtractData(tx, tokenEvent)
}

func (this *VCCBlockScanner) MakeSimpleTxFromExtractData(tx *ethereum.BlockTransaction) (string, []*openwallet.TxExtractData, error) {
	var sourceKey string
	var exist bool
	var extractDataList []*openwallet.TxExtractData
	if sourceKey, exist = tx.FilterFunc(tx.From); !exist { //this.GetSourceKeyByAddress(tx.From)
		return "", extractDataList, nil
	}

	feeprice, err := tx.GetTxFeeEthString()
	if err != nil {
		this.wm.Log.Errorf("calc tx fee in eth failed, err=%v", err)
		return "", extractDataList, err
	}

	amountVal, err := tx.GetAmountEthString()
	if err != nil {
		this.wm.Log.Errorf("calc amount to eth decimal failed, err=%v", err)
		return "", extractDataList, err
	}

	nowUnix := time.Now().Unix()

	deductTxInput := openwallet.TxInput{
		Recharge: openwallet.Recharge{
			Sid:      openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), "", 0), //base64.StdEncoding.EncodeToString(crypto.SHA1([]byte(fmt.Sprintf("input_%s_%d_%s", tx.Hash, 0, tx.From)))),
			CreateAt: nowUnix,
			TxID:     tx.Hash,
			Address:  tx.From,
			Coin: openwallet.Coin{
				Symbol:     this.wm.Symbol(),
				IsContract: false,
			},
			Amount:      amountVal,
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxType:      0,
		},
	}

	feeTxInput := openwallet.TxInput{
		Recharge: openwallet.Recharge{
			Sid:      openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), "", 1), //base64.StdEncoding.EncodeToString(crypto.SHA1([]byte(fmt.Sprintf("input_%s_%d_%s", tx.Hash, 0, tx.From)))),
			CreateAt: nowUnix,
			TxID:     tx.Hash,
			Address:  tx.From,
			Coin: openwallet.Coin{
				Symbol:     this.wm.Symbol(),
				IsContract: false,
			},
			Amount:      feeprice,
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxType:      0,
		},
	}

	from := []string{
		tx.From + ":" + amountVal,
	}
	to := []string{
		tx.To + ":" + amountVal,
	}

	transx := &openwallet.Transaction{
		WxID:        openwallet.GenTransactionWxID2(tx.Hash, this.wm.Symbol(), ""),
		TxID:        tx.Hash,
		From:        from,
		To:          to,
		Decimal:     18,
		BlockHash:   tx.BlockHash,
		BlockHeight: tx.BlockHeight,
		Fees:        feeprice, //tx.GasPrice, //totalSpent.Sub(totalReceived).StringFixed(8),
		Coin: openwallet.Coin{
			Symbol:     this.wm.Symbol(),
			IsContract: false,
		},
		SubmitTime:  nowUnix,
		ConfirmTime: nowUnix,
		Status:      common.NewString(tx.Status).String(),
		TxType:      0,
	}

	txExtractData := &openwallet.TxExtractData{}
	txExtractData.TxInputs = append(txExtractData.TxInputs, &deductTxInput)
	txExtractData.TxInputs = append(txExtractData.TxInputs, &feeTxInput)
	txExtractData.Transaction = transx

	extractDataList = append(extractDataList, txExtractData)

	return sourceKey, extractDataList, nil
}

func (this *VCCBlockScanner) MakeTokenTxFromExtractData(tx *ethereum.BlockTransaction, tokenEvent *ethereum.TransferEvent) (string, []*openwallet.TxExtractData, error) {
	var sourceKey string
	var exist bool
	var extractDataList []*openwallet.TxExtractData
	if sourceKey, exist = tx.FilterFunc(tokenEvent.TokenFrom); !exist { //this.GetSourceKeyByAddress(tokenEvent.TokenFrom)
		return "", extractDataList, nil
	}

	contractId := openwallet.GenContractID(this.wm.Symbol(), tx.To) //base64.StdEncoding.EncodeToString(crypto.SHA256([]byte(fmt.Sprintf("{%v}_{%v}", this.wm.Symbol(), tx.To))))
	nowUnix := time.Now().Unix()

	coin := openwallet.Coin{
		Symbol:     this.wm.Symbol(),
		IsContract: true,
		ContractID: contractId,
		Contract: openwallet.SmartContract{
			ContractID: contractId,
			Address:    tx.To,
			Symbol:     this.wm.Symbol(),
		},
	}

	feeprice, err := tx.GetTxFeeEthString()
	if err != nil {
		this.wm.Log.Errorf("calc tx fee in eth failed, err=%v", err)
		return "", extractDataList, err
	}

	tokenValue, err := ethereum.ConvertToBigInt(tokenEvent.Value, 16)
	if err != nil {
		this.wm.Log.Errorf("convert token value to big.int failed, err=%v", err)
		return "", extractDataList, err
	}

	deductTxInput := openwallet.TxInput{
		Recharge: openwallet.Recharge{
			Sid:         openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), contractId, 0), //base64.StdEncoding.EncodeToString(crypto.SHA1([]byte(fmt.Sprintf("input_%s_%d_%s", tx.Hash, 0, tx.From)))),
			CreateAt:    nowUnix,
			TxID:        tx.Hash,
			Address:     tx.From,
			Coin:        coin,
			Amount:      tokenValue.String(),
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxType:      0,
		},
	}
	from := []string{
		tokenEvent.TokenFrom + ":" + tokenValue.String(),
	}
	to := []string{
		tokenEvent.TokenTo + ":" + tokenValue.String(),
	}

	tokentransx := &openwallet.Transaction{
		WxID: openwallet.GenTransactionWxID2(tx.Hash, this.wm.Symbol(), contractId),
		TxID: tx.Hash,
		From: from,
		To:   to,
		//		Decimal:     18,
		BlockHash:   tx.BlockHash,
		BlockHeight: tx.BlockHeight,
		Fees:        "0", //tx.GasPrice, //totalSpent.Sub(totalReceived).StringFixed(8),
		Coin:        coin,
		SubmitTime:  nowUnix,
		ConfirmTime: nowUnix,
		Status:      common.NewString(tx.Status).String(),
		TxType:      0,
	}

	tokenTransExtractData := &openwallet.TxExtractData{}
	tokenTransExtractData.Transaction = tokentransx
	tokenTransExtractData.TxInputs = append(tokenTransExtractData.TxInputs, &deductTxInput)

	feeTxInput := openwallet.TxInput{
		Recharge: openwallet.Recharge{
			Sid:      openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), "", 0), //base64.StdEncoding.EncodeToString(crypto.SHA1([]byte(fmt.Sprintf("input_%s_%d_%s", tx.Hash, 0, tokenEvent.TokenFrom)))),
			CreateAt: nowUnix,
			TxID:     tx.Hash,
			Address:  tx.From,
			Coin: openwallet.Coin{
				Symbol:     this.wm.Symbol(),
				IsContract: false,
			},
			Amount:      feeprice,
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxType:      1,
		},
	}
	from = []string{
		tx.From + ":" + "0",
	}
	to = []string{
		tx.To + ":" + "0",
	}

	feeTx := &openwallet.Transaction{
		WxID:        openwallet.GenTransactionWxID2(tx.Hash, this.wm.Symbol(), ""),
		TxID:        tx.Hash,
		From:        from,
		To:          to,
		Decimal:     18,
		BlockHash:   tx.BlockHash,
		BlockHeight: tx.BlockHeight,
		Fees:        feeprice, //tx.GasPrice, //totalSpent.Sub(totalReceived).StringFixed(8),
		Coin: openwallet.Coin{
			Symbol:     this.wm.Symbol(),
			IsContract: false,
		},
		SubmitTime:  nowUnix,
		ConfirmTime: nowUnix,
		Status:      common.NewString(tx.Status).String(),
		TxType:      1,
	}

	feeExtractData := &openwallet.TxExtractData{}
	feeExtractData.Transaction = feeTx
	feeExtractData.TxInputs = append(feeExtractData.TxInputs, &feeTxInput)

	extractDataList = append(extractDataList, tokenTransExtractData)
	extractDataList = append(extractDataList, feeExtractData)

	return sourceKey, extractDataList, nil
}

//ExtractTransactionData 扫描一笔交易
func (this *VCCBlockScanner) ExtractTransactionData(txid string, scanTargetFunc openwallet.BlockScanTargetFunc) (map[string][]*openwallet.TxExtractData, error) {
	//result := bs.ExtractTransaction(0, "", txid, scanAddressFunc)
	tx, err := this.wm.WalletClient.EthGetTransactionByHash(txid)
	if err != nil {
		this.wm.Log.Errorf("get transaction by has failed, err=%v", err)
		return nil, fmt.Errorf("get transaction by has failed, err=%v", err)
	}
	scanAddressFunc := func(address string) (string, bool) {
		target := openwallet.ScanTarget{
			Address:          address,
			BalanceModelType: openwallet.BalanceModelTypeAddress,
		}
		return scanTargetFunc(target)
	}
	tx.FilterFunc = scanAddressFunc
	result, err := this.TransactionScanning(tx)
	if err != nil {
		this.wm.Log.Errorf("scan transaction[%v] failed, err=%v", txid, err)
		return nil, fmt.Errorf("scan transaction[%v] failed, err=%v", txid, err)
	}
	return result.extractData, nil
}

func (this *VCCBlockScanner) TransactionScanning(tx *ethereum.BlockTransaction) (*ExtractResult, error) {
	//txToNotify := make(map[string][]ethereum.BlockTransaction)
	if tx.BlockNumber == "" {
		return &ExtractResult{
			BlockHeight: 0,
			TxID:        "",
			extractData: make(map[string][]*openwallet.TxExtractData),
			Success:     true,
		}, nil
	}

	blockHeight, err := ethereum.ConvertToUint64(tx.BlockNumber, 16)
	if err != nil {
		this.wm.Log.Errorf("convert block number from string to uint64 failed, err=%v", err)
		return nil, err
	}

	tx.BlockHeight = blockHeight
	var result = ExtractResult{
		BlockHeight: blockHeight,
		TxID:        tx.BlockHash,
		extractData: make(map[string][]*openwallet.TxExtractData),
		Success:     true,
	}

	if this.WalletDAI != nil {
		//VCC的from地址匿名需要查钱包系统的数据库中的交易单
		txsDB, _ := this.WalletDAI.GetTransactionByTxID(tx.Hash, this.wm.Symbol())

		if txsDB != nil {
			for _, txDB := range txsDB {
				for _, f := range txDB.From {
					fv := strings.Split(f, ":")
					if len(fv) == 2 {
						tx.From = fv[0]
						break
					}
				}
			}
		}
	}

	tokenEvent, err := this.UpdateTxByReceipt(tx)
	if err != nil && strings.Index(err.Error(), "result type is Null") == -1 {
		this.wm.Log.Errorf("UpdateTxByReceipt failed, err=%v", err)
		return nil, err
	} else if err != nil && strings.Index(err.Error(), "result type is Null") != -1 {
		err = this.wm.SaveUnscannedTransaction(tx, "get tx receipt reply with null result")
		if err != nil {
			this.wm.Log.Errorf("block height: %d, save unscan record failed. unexpected error: %v", tx.BlockHeight, err)
			return nil, err
		}
		return &result, nil
	}

	//FromSourceKey, fromExtractDataList, err := this.MakeFromExtractData(tx, tokenEvent)
	//if err != nil {
	//	this.wm.Log.Errorf("MakeFromExtractData failed, err=%v", err)
	//	return nil, err
	//}
	//
	//ToSourceKey, toExtractDataList, err := this.MakeToExtractData(tx, tokenEvent)
	//if err != nil {
	//	this.wm.Log.Errorf("MakeToExtractData failed, err=%v", err)
	//	return nil, err
	//}
	//
	//if FromSourceKey == ToSourceKey && FromSourceKey != "" {
	//	for i, _ := range fromExtractDataList {
	//		for j, _ := range toExtractDataList {
	//			if fromExtractDataList[i].Transaction.To[0] == toExtractDataList[j].Transaction.To[0] {
	//				fromExtractDataList[i].TxOutputs = toExtractDataList[j].TxOutputs
	//			}
	//		}
	//	}
	//
	//	result.extractData[FromSourceKey] = fromExtractDataList
	//} else if FromSourceKey != "" && ToSourceKey != "" {
	//	result.extractData[FromSourceKey] = fromExtractDataList
	//	result.extractData[ToSourceKey] = toExtractDataList
	//} else if FromSourceKey != "" {
	//	result.extractData[FromSourceKey] = fromExtractDataList
	//} else if ToSourceKey != "" {
	//	result.extractData[ToSourceKey] = toExtractDataList
	//}

	isTokenTransfer := false
	if len(tokenEvent) > 0 {
		isTokenTransfer = true
	}

	//提出主币交易单
	extractData, err := this.extractETHTransaction(tx, isTokenTransfer)
	if err != nil {
		return nil, err
	}
	for sourceKey, data := range extractData {
		extractDataArray := result.extractData[sourceKey]
		if extractDataArray == nil {
			extractDataArray = make([]*openwallet.TxExtractData, 0)
		}
		extractDataArray = append(extractDataArray, data)
		result.extractData[sourceKey] = extractDataArray
	}

	//提取代币交易单
	for contractAddress, tokenEventArray := range tokenEvent {
		//提出主币交易单
		extractERC20Data, err := this.extractERC20Transaction(tx, contractAddress, tokenEventArray)
		if err != nil {
			return nil, err
		}
		for sourceKey, data := range extractERC20Data {
			extractDataArray := result.extractData[sourceKey]
			if extractDataArray == nil {
				extractDataArray = make([]*openwallet.TxExtractData, 0)
			}
			extractDataArray = append(extractDataArray, data)
			result.extractData[sourceKey] = extractDataArray
		}
	}

	return &result, nil
}


//extractETHTransaction 提取ETH主币交易单
func (this *VCCBlockScanner) extractETHTransaction(tx *ethereum.BlockTransaction, isTokenTransfer bool) (map[string]*openwallet.TxExtractData, error) {

	txExtractMap := make(map[string]*openwallet.TxExtractData)
	from := tx.From
	to := tx.To
	status := "1"
	reason := ""
	nowUnix := time.Now().Unix()
	txType := uint64(0)

	coin := openwallet.Coin{
		Symbol:     this.wm.Symbol(),
		IsContract: false,
	}

	if isTokenTransfer {
		txType = 1
	}

	ethAmount, err := tx.GetAmountEthString()
	if err != nil {
		return nil, err
	}

	feeprice, err := tx.GetTxFeeEthString()
	if err != nil {
		return nil, err
	}

	sourceKey, ok := tx.FilterFunc(from)
	if ok {
		input := &openwallet.TxInput{}
		input.TxID = tx.Hash
		input.Address = from
		input.Amount = ethAmount
		input.Coin = coin
		input.Index = 0
		input.Sid = openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), "", 0)
		input.CreateAt = nowUnix
		input.BlockHeight = tx.BlockHeight
		input.BlockHash = tx.BlockHash
		input.TxType = txType

		//transactions = append(transactions, &transaction)

		ed := txExtractMap[sourceKey]
		if ed == nil {
			ed = openwallet.NewBlockExtractData()
			txExtractMap[sourceKey] = ed
		}

		ed.TxInputs = append(ed.TxInputs, input)

		//手续费作为一个输入
		feeInput := &openwallet.TxInput{}
		feeInput.Recharge.Sid = openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), "", uint64(1))
		feeInput.Recharge.TxID = tx.Hash
		feeInput.Recharge.Address = from
		feeInput.Recharge.Coin = coin
		feeInput.Recharge.Amount = feeprice
		feeInput.Recharge.BlockHash = tx.BlockHash
		feeInput.Recharge.BlockHeight = tx.BlockHeight
		feeInput.Recharge.Index = 1 //账户模型填0
		feeInput.Recharge.CreateAt = nowUnix
		feeInput.Recharge.TxType = txType

		ed.TxInputs = append(ed.TxInputs, feeInput)

	}

	sourceKey2, ok2 := tx.FilterFunc(to)
	if ok2 {
		output := &openwallet.TxOutPut{}
		output.TxID = tx.Hash
		output.Address = to
		output.Amount = ethAmount
		output.Coin = coin
		output.Index = 0
		output.Sid = openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), "", 0)
		output.CreateAt = nowUnix
		output.BlockHeight = tx.BlockHeight
		output.BlockHash = tx.BlockHash
		output.TxType = txType

		ed := txExtractMap[sourceKey2]
		if ed == nil {
			ed = openwallet.NewBlockExtractData()
			txExtractMap[sourceKey2] = ed
		}

		ed.TxOutputs = append(ed.TxOutputs, output)
	}

	for _, extractData := range txExtractMap {

		tx := &openwallet.Transaction{
			Fees:        feeprice,
			Coin:        coin,
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxID:        tx.Hash,
			Decimal:     this.wm.Decimal(),
			Amount:      ethAmount,
			ConfirmTime: nowUnix,
			From:        []string{from + ":" + ethAmount},
			To:          []string{to + ":" + ethAmount},
			Status:      status,
			Reason:      reason,
			TxType:      txType,
		}

		wxID := openwallet.GenTransactionWxID(tx)
		tx.WxID = wxID
		extractData.Transaction = tx

	}
	return txExtractMap, nil
}

//extractERC20Transaction
func (this *VCCBlockScanner) extractERC20Transaction(tx *ethereum.BlockTransaction, contractAddress string, tokenEvent []*ethereum.TransferEvent) (map[string]*openwallet.TxExtractData, error) {

	nowUnix := time.Now().Unix()
	status := "1"
	reason := ""
	txExtractMap := make(map[string]*openwallet.TxExtractData)

	contractId := openwallet.GenContractID(this.wm.Symbol(), contractAddress)
	coin := openwallet.Coin{
		Symbol:     this.wm.Symbol(),
		IsContract: true,
		ContractID: contractId,
		Contract: openwallet.SmartContract{
			ContractID: contractId,
			Address:    contractAddress,
			Symbol:     this.wm.Symbol(),
		},
	}

	//提取出账部分记录
	from, err := this.extractERC20Detail(tx, contractAddress, tokenEvent, true, txExtractMap)
	if err != nil {
		return nil, err
	}
	//提取入账部分记录
	to, err := this.extractERC20Detail(tx, contractAddress, tokenEvent, false, txExtractMap)
	if err != nil {
		return nil, err
	}

	for _, extractData := range txExtractMap {
		tx := &openwallet.Transaction{
			Fees:        "0",
			Coin:        coin,
			BlockHash:   tx.BlockHash,
			BlockHeight: tx.BlockHeight,
			TxID:        tx.Hash,
			Amount:      "0",
			ConfirmTime: nowUnix,
			From:        from,
			To:          to,
			Status:      status,
			Reason:      reason,
			TxType:      0,
		}

		wxID := openwallet.GenTransactionWxID(tx)
		tx.WxID = wxID
		extractData.Transaction = tx

	}
	return txExtractMap, nil
}

//extractERC20Detail
func (this *VCCBlockScanner) extractERC20Detail(tx *ethereum.BlockTransaction, contractAddress string, tokenEvent []*ethereum.TransferEvent, isInput bool, extractData map[string]*openwallet.TxExtractData) ([]string, error) {

	var (
		addrs  = make([]string, 0)
		txType = uint64(0)
	)

	contractId := openwallet.GenContractID(this.wm.Symbol(), contractAddress)
	coin := openwallet.Coin{
		Symbol:     this.wm.Symbol(),
		IsContract: true,
		ContractID: contractId,
		Contract: openwallet.SmartContract{
			ContractID: contractId,
			Address:    contractAddress,
			Symbol:     this.wm.Symbol(),
		},
	}

	createAt := time.Now().Unix()
	for i, te := range tokenEvent {

		address := ""
		if isInput {
			address = te.TokenFrom
		} else {
			address = te.TokenTo
		}

		tokenValue, err := ethereum.ConvertToBigInt(te.Value, 16)
		if err != nil {
			return nil, err
		}

		sourceKey, ok := tx.FilterFunc(address)
		if ok {

			detail := openwallet.Recharge{}
			detail.Sid = openwallet.GenTxInputSID(tx.Hash, this.wm.Symbol(), coin.ContractID, uint64(i))
			detail.TxID = tx.Hash
			detail.Address = address
			detail.Coin = coin
			detail.Amount = tokenValue.String()
			detail.BlockHash = tx.BlockHash
			detail.BlockHeight = tx.BlockHeight
			detail.Index = uint64(i) //账户模型填0
			detail.CreateAt = createAt
			detail.TxType = txType

			ed := extractData[sourceKey]
			if ed == nil {
				ed = openwallet.NewBlockExtractData()
				extractData[sourceKey] = ed
			}

			if isInput {
				txInput := &openwallet.TxInput{Recharge: detail}
				ed.TxInputs = append(ed.TxInputs, txInput)
			} else {
				txOutPut := &openwallet.TxOutPut{Recharge: detail}
				ed.TxOutputs = append(ed.TxOutputs, txOutPut)
			}

		}

		addrs = append(addrs, address+":"+tokenValue.String())

	}
	return addrs, nil
}


//GetScannedBlockHeader 获取当前已扫区块高度
func (this *VCCBlockScanner) GetScannedBlockHeader() (*openwallet.BlockHeader, error) {

	var (
		blockHeight uint64 = 0
		hash        string
		err         error
	)

	blockHeight, hash, err = this.wm.GetLocalNewBlock()
	if err != nil {
		this.wm.Log.Errorf("get local new block failed, err=%v", err)
		return nil, err
	}

	//如果本地没有记录，查询接口的高度
	if blockHeight == 0 {
		blockHeight, err = this.wm.WalletClient.EthGetBlockNumber()
		if err != nil {
			this.wm.Log.Errorf("EthGetBlockNumber failed, err=%v", err)
			return nil, err
		}

		//就上一个区块链为当前区块
		blockHeight = blockHeight - 1

		block, err := this.wm.WalletClient.EthGetBlockSpecByBlockNum(blockHeight, false)
		if err != nil {
			this.wm.Log.Errorf("get block spec by block number failed, err=%v", err)
			return nil, err
		}
		hash = block.BlockHash
	}

	return &openwallet.BlockHeader{Height: blockHeight, Hash: hash}, nil
}

//GetCurrentBlockHeader 获取当前区块高度
func (this *VCCBlockScanner) GetCurrentBlockHeader() (*openwallet.BlockHeader, error) {

	var (
		blockHeight uint64 = 0
		hash        string
		err         error
	)

	blockHeight, err = this.wm.WalletClient.EthGetBlockNumber()
	if err != nil {
		this.wm.Log.Errorf("EthGetBlockNumber failed, err=%v", err)
		return nil, err
	}

	block, err := this.wm.WalletClient.EthGetBlockSpecByBlockNum(blockHeight, false)
	if err != nil {
		this.wm.Log.Errorf("get block spec by block number failed, err=%v", err)
		return nil, err
	}
	hash = block.BlockHash

	return &openwallet.BlockHeader{Height: blockHeight, Hash: hash}, nil
}

func (this *VCCBlockScanner) GetGlobalMaxBlockHeight() uint64 {

	maxBlockHeight, err := this.wm.WalletClient.EthGetBlockNumber()
	if err != nil {
		this.wm.Log.Errorf("get max height of eth failed, err=%v", err)
		return 0
	}
	return maxBlockHeight
}
