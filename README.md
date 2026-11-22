# PPanel-node

A PPanel node server based on xray-core, modified from v2node.  
一个基于xray内核的PPanel节点服务端，修改自v2node

## 软件安装

### 一键安装

```
wget -N https://raw.githubusercontent.com/lolitia-connect/node-backend/master/scripts/install.sh && bash install.sh
```

A node can run multiple protocol instances at the same time. When only one protocol instance changes, the node hot-reloads that instance's listener and keeps the other protocol listeners running. Changes to shared core settings still use the safe full-core replacement path.

一个节点可以同时运行多个协议实例。仅修改某个协议实例时，只热重载该实例的监听，其他协议监听保持运行；共享核心配置发生变化时，仍使用安全的整核心替换流程。

## 构建
``` bash
go build -v -o ./output/ppnode -trimpath -ldflags "-s -w -buildid="
```
