module github.com/assetsadapterstore/cryptozoic-adapter

go 1.12

require (
	github.com/asdine/storm v2.1.2+incompatible
	github.com/astaxie/beego v1.11.1
	github.com/blocktree/ethereum-adapter v1.3.0
	github.com/blocktree/openwallet v1.5.5
)

//replace github.com/blocktree/ethereum-adapter => ../ethereum-adapter

//replace github.com/blocktree/openwallet => ../../openwallet
