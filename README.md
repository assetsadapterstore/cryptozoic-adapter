# cryptozoic-adapter

本项目继承了ethereum-adapter，主要修改了如下内容：
   
- 重写了Symbol = "VCC"。

## 如何测试

openwtester包下的测试用例已经集成了openwallet钱包体系，创建conf文件，新建VCC.ini文件，编辑如下内容：

```ini

#wallet api url
ServerAPI = "http://127.0.0.1:20035"

#block chain ID
ChainID = 

# Cache data file directory, default = "", current directory: ./data
dataDir = ""

```
