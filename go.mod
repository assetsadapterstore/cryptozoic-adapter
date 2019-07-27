module github.com/assetsadapterstore/cryptozoic-adapter

go 1.12

require (
	github.com/asdine/storm v2.1.2+incompatible
	github.com/astaxie/beego v1.11.1
	github.com/blocktree/ethereum-adapter v1.2.2
	github.com/blocktree/openwallet v1.4.9
)

//replace github.com/blocktree/ethereum-adapter => ../ethereum-adapter

//replace github.com/blocktree/openwallet => ../../openwallet
