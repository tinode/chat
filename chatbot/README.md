# Tinode Chatbot Example

This is a rudimentary chatbot for Tinode using [gRPC API](../pbx/). It's written in Python as a demonstration
that the API is language-independent.

The chat bot subscribes to events stream using Plugin API and logs in as 'Tino the Chatbot' user. The event stream API is used to listen for new accounts. When a new account is created, the bot initiates a p2p topic with the new user. Then it listens for messages sent to the topic and responds to each with a random quote.

Generated files are provided for convenience. You may re-generate them if needed:
```
python -m grpc_tools.protoc -I../pbx --python_out=. --grpc_python_out=. ../pbx/model.proto
```

## Installing and running

Make sure [python](https://www.python.org/) 2.7 or 3.4 or higher is installed. Make sure [pip](https://pip.pypa.io/en/stable/installing/) 9.0.1 or higher is installed. If you are using python 2.7 install `futures`:
```
pip install futures
```
Follow instructions to [install grpc](https://grpc.io/docs/quickstart/python.html#install-grpc). The package is called `grpcio`:
```
pip install grpcio
```

Start the [tinode server](../server/) first. Then start the chatbot under the user you want to act as your bot, `alice` in this example:
```
python chatbot.py --login-basic=alice:alice123
```

You can use cookie file to store credentials. Sample cookie files are provided as `basic-cookie.sample` and `token-cookie.sample`. Once authenticated the bot will attempt to store token in the provided cookie file, `.tn-cookie` by default. If you have a cookie file with the default name, you can run the bot with no parameters:

```
python chatbot.py
```
