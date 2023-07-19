# server 笔记
熟悉的功能：
- [x] websocket
[x] 集群选举
[x] 一致性hash
[x] 雪花算法
[x] 文件上传/下载
[x] Topic处理数据流程
[] 视频通话
## 文件夹说明
```
server
├── auth                        # 认证，身份验证器
├── concurrency                 # 协程池，锁
├── db                          # 数据库适配器（mongodb，mysql，postgres，rethinkdb）
├── drafty                      # 转换文本工具, 转换json文本等
├── logs                        # 日志记录
├── media                       # 上传/下载媒体
├── push                        # 推送相关工具（tnpg, fcm）
├── ringhash                    # 一致性hash
├── store                       # 注册和访问数据库的方法
├── templ                       # 模板文件
└── validate                    # 验证数据（手机号，email）
```
## 文件说明
```
apikey.go         # 校验key, 该key使用apikeyVersion + apikeyAppID + apikeySequence + apikeyWho + apikeySignature规则和盐算法计算得出, 主要用于用户,客户端等与服务器做验证
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
pres.go           # 用户订阅, 通知相关
push.go           # 订阅推送通知
session.go        # 会话管理，一个用户可以拥有多个会话，一个会话拥有多个topic
sessionstore.go   # 会话存储
stats.go          # 服务状态
topic_proxy.go    # topic代理
topic.go          # topic，用户，房间会话，通信，使用channle用于传递用户消息
user.go           # 用户信息
utils.go          # 字符串, 数组, IP等操作工具集
```


## raft
https://juejin.cn/post/7143541597165060109
http://thesecretlivesofdata.com/raft/
https://here2say.com/44/
https://raft.github.io/
CAP理论：一个分布式系统最多只能同时满足一致性（Consistency）、可用性（Availability）和分区容错性（Partition tolerance）这三项中的两项。
```
可以看出 Raft 协议都能很好的应对一致性问题，并且很容易理解。
Raft一共分为三种角色，leader，follower，candidate，字如其名
选举倒计时timeout通常是 150ms ~ 300ms 直接的某个随机值
集群启动时，没有leader，而是全部初始化为 follower
多个leader出现的时候，对比他们的term即任期（上面的步长），新者为leader
数据的流向只能从 Leader 节点向 Follower 节点转移
数据在大多数节点提交后才commit
```
## websocket
建立链接
解析信息存储 session,uid
协程读写
关闭连接

## snowflake 
用于uid
时间戳+机器id+序列号
本项目中的机器id取的是集群数组的index
## lru
用于session

具体步骤如下：

当需要访问数据时，首先在缓存中查找该数据。
如果数据存在于缓存中，将其标记为最近使用，移动到链表头部或更新哈希表中的位置。
如果数据不存在于缓存中，需要从主存或磁盘中获取数据，同时检查缓存是否已满。
如果缓存已满，选择链表尾部的数据进行淘汰，即最久未使用的数据，然后将新数据插入到链表头部或哈希表中。
如果缓存未满，直接将新数据插入到链表头部或哈希表中。
通过不断地在缓存中记录最近使用的数据，并将最近使用的数据放在链表或哈希表的头部，LRU算法可以保留最常使用的数据，提高缓存的命中率。

需要注意的是，LRU算法的实现可以根据具体需求进行优化和改进，例如**结合哈希表和双向链表**的方式，以提高访问效率。此外，还可以通过设置合适的缓存大小来平衡空间利用率和缓存命中率。

## 一致性hash
下面是一致性哈希算法的基本原理：

将哈希空间映射到一个环形环上（数组模拟）。
将节点的标识（例如IP地址、主机名）进行哈希计算，并将其映射到环上的位置。
将每个节点复制多次（虚拟节点）放置在环上，增加数据在环上的均匀分布性。
根据数据的哈希值（与数组长度做&运算），将数据映射到环上的位置。
在环上顺时针寻找最近的节点，将数据存储在该节点上。
当需要添加或删除节点时，只影响到该节点附近的数据，而不会触发整个哈希环的重映射。这样可以最大程度地减少缓存失效，并且能够实现负载均衡。

一致性哈希算法在分布式缓存系统、负载均衡和分布式数据库中得到广泛应用。它不仅能够提供高效的数据访问，还具备较好的可伸缩性和容错性。

## 文件上传/下载
上传：
获取文件流
拷贝文件流
读取文件信息
写入本地文件/指定存储位置
数据库存取文件信息

下载：
数据库获取文件信息
找到文件位置
读取获取文件流
响应文件流

## Topic 和 Group Topic
每一个用户都有自己的Topic（channel实现），用于处理数据{me},{pres},{note}等，希望和默认聊天，则只需要往指定对象的topic放入数据即可
当用户发起视频通话时，需要开启一个新的topic用于单独处理p2p，topic的名字是用户的名字


## topic处理流程
topic处理数据流程：websocket读取数据{me},{pub},{sub}等->dispatch->session-放入hub：join-> 取出hub：join数据，初始化topic,并放入注册数据->topic启动run->读取注册数据->处理sub订阅数据，放入session:map存储订阅者
websocket读取数据->dispatch->解析数据，从session:map取出对应的topic->将数据放入topic对应的channel-> channle接受数据发送给所有的订阅者->由queueOut方法将数据放入session中的名字send的channel中, 后续读取channle数据Websock写回数据
