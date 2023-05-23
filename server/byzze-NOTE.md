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



