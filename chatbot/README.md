# Tinode Chatbot Example

This is a rudimentary chatbot for Tinode using [gRPC API](../pbx/). It's written in Python as a demonstration
that the API is language-independent.

The chat bot listens to messages sent to topic X and responds to each with a random quote.

Generated files are provided for convenience. You may re-generate them if needed:
```
python -m grpc_tools.protoc -I../pbx --python_out=. --grpc_python_out=. ../pbx/model.proto
```
