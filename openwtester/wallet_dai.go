/*
 * Copyright 2019 The openwallet Authors
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

package openwtester

import (
	"github.com/blocktree/openwallet/openw"
	"github.com/blocktree/openwallet/openwallet"
)

type walletWrapper struct {
	*openwallet.WalletDAIBase
	wm *openw.WalletManager
}

//GetTransactionByTxID
func (wrapper *walletWrapper) GetTransactionByTxID(txid, symbol string) ([]*openwallet.Transaction, error) {
	result := make([]*openwallet.Transaction, 0)
	txs, _ := wrapper.wm.GetTransactions(testApp, 0, 200, "TxID", txid)
	if txs != nil {
		for _, tx := range txs {
			result = append(result, tx)
		}
	}

	return txs, nil
}
