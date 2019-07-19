package openwtester

import (
	"github.com/assetsadapterstore/cryptozoic-adapter/cryptozoic"
	"github.com/blocktree/openwallet/log"
	"github.com/blocktree/openwallet/openw"
	"path/filepath"
)

var (
	testApp        = "cryptozoic-adapter"
	configFilePath = filepath.Join("conf")
)

func init() {
	//注册钱包管理工具
	log.Notice("Wallet Manager Load Successfully.")
	openw.RegAssets(cryptozoic.Symbol, cryptozoic.NewWalletManager())
}
