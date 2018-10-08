# grpc-tools generates python 2 file which does not work with
# python3 packaging system. This is a fix.

model_pb2_grpc = "../py_grpc/tinode_grpc/model_pb2_grpc.py"

with open(model_pb2_grpc, "r") as fh:
    content = fh.read().replace("\nimport model_pb2 as model__pb2",
        "\nfrom . import model_pb2 as model__pb2")

    with open(model_pb2_grpc,"w") as fh:
        fh.write(content)
