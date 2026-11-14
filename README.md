# PPanel-node

A PPanel node server based on xray-core, modified from v2node.  
一个基于xray内核的PPanel节点服务端，修改自v2node

## 软件安装

### 一键安装

```
wget -N https://raw.githubusercontent.com/lolitia-connect/node-backend/master/scripts/install.sh && bash install.sh
```

## 构建
``` bash
go build -v -o ./output/ppnode -trimpath -ldflags "-s -w -buildid="
```
