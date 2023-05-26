# server 笔记

## 项目文件夹
server
├── auth                        # 认证，身份验证器
├── concurrency                 # 协程池，锁
├── db                          # 数据库适配器（mongodb，mysql，postgres，rethinkdb）
├── drafty                      # 转换文本工具
├── logs                        # 日志记录
├── media                       # 上传/下载媒体
├── push                        # 推送相关工具
├── ringhash                    # 一致性hash
├── store                       # 注册和访问数据库的方法
├── templ                       # 模板文件
└── validate                    # 验证数据（手机号，email）

apikey.go # 校验key, 该key使用apikeyVersion + apikeyAppID + apikeySequence + apikeyWho + apikeySignature规则和盐算法计算得出, 主要用于用户,客户端等与服务器验证
calls.go          # 视频调用
cluster_leader.go # 集群leader，raft协议
cluster.go        # 集群配置,读取节点，健康检查
datamodel.go      # 定义数据结构体 acc，login，client
hdl_files.go      # 处理大文件
hdl_grpc.go       # 处理grpc
hdel_longpoll.go  # 处理长轮询
hdl_wensock.go    # 处理websocket
http_pprof.go     # debug启用pprof分析性能
http.go           # web服务相关，启动，状态，404等
hub.go            # topic 创将/断开，重连机制
init.topic.go     # topic 初始化
main.go           # 程序主入口
pbconverter.go    # pb协议和go结构体转换
plugins.go        # 插件相关
pres.go           #
push.go           # 订阅推送通知
session.go        # 会话管理，一个用户可以拥有多个会话，一个会话拥有多个topic
sessionstore.go   # 会话存储
stats.go          # 服务状态
topic_proxy.go    # topic代理
topic.go          # topic，用户，房间会话，通信
user.go           # 用户信息
utils.go


