module github.com/assetsadapterstore/cryptozoic-adapter

go 1.12

require (
	github.com/asdine/storm v2.1.2+incompatible
	github.com/astaxie/beego v1.12.0
	github.com/blocktree/ethereum-adapter v1.4.0
	github.com/blocktree/openwallet v1.7.0
)

//replace github.com/blocktree/ethereum-adapter => ../ethereum-adapter

//replace github.com/blocktree/openwallet => ../../openwallet
