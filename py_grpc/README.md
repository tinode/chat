# Generated Protocol Buffer and gRPC files for [Tinode](https://github.com/tinode)

Generated Python code for [gRPC](https://grpc.io/) client and plugins.

gRPC clients must implement rpc service `Node`, plugins must implement `Plugin`.

For a sample implementation of a command line client see [tn-cli](https://github.com/tinode/chat/tree/master/tn-cli/).
For a partial plugin implementation see [chatbot](https://github.com/tinode/chat/tree/master/chatbot).

## Installing

Install the package by executing
```
pip install tinode_grpc
```


## Generating files

Don't modify included files directly. If you want to make changes, you have to install protobuffers tool chain and gRPC then generate the Python bindings from [`pbx/model.proto`](https://github.com/tinode/chat/tree/master/pbx/model.proto) (your path to `model.proto` may be different):
```
pip install grpcio-tools
python -m grpc_tools.protoc -I../pbx --python_out=. --grpc_python_out=. ../pbx/model.proto
```
The generated `model_pb2_grpc.py` imports `model_pb2.py` as a module instead of a package which is incompatible with python3 packaging system. Use `../pbx/py_fix.py` to apply a fix. This is only needed if you want to repackage the generated files.


## 笔记
py_grpc项目下的文件表示的含义
setup.py # 将../pbx/model.proto协议生成python代码,并将其封装成一个文件夹,导出供外部人员调用proto协议的方法
version # 将`git describe --tags`生成的版本写入`tinode_grpc/GIT_VERSION`文件中