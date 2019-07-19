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

package cryptozoic

import (
	"github.com/blocktree/openwallet/log"
	"testing"
)

func TestWalletManager_EthGetTransactionByHash(t *testing.T) {
	txid := "0x3f3945ccc8b73e622b2f74623d139df9e9232e255e42e8d7511973343aa49e31"
	tx, err := tw.WalletClient.EthGetTransactionByHash(txid)
	if err != nil {
		t.Errorf("get transaction by has failed, err=%v", err)
		return
	}
	log.Infof("tx: %+v", tx)
}