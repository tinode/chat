# Tinode Chatbot Example

This is a rudimentary chatbot for Tinode using [gRPC API](../pbx/). It's written in Python as a demonstration
that the API is language-independent.

The chat bot subscribes to events stream using Plugin API and logs in as 'Tino the Chatbot' user. The event stream API is used to listen for new accounts. When a new account is created, the bot initiates a p2p topic with the new user. Then it listens for messages sent to the topic and responds to each with a random quote from `quotes.txt` file.

Generated files are provided for convenience in a [separate folder](../pbx). You may re-generate them if needed:
```
python -m grpc_tools.protoc -I../pbx --python_out=. --grpc_python_out=. ../pbx/model.proto
```

## Installing and running

### Using Docker

**Warning!** Although the chatbot itself is less than 100KB, the chatbot Docker image is 175MB: the `:slim` Python 3 image is about 140MB, gRPC adds another ~30MB.

1. Follow [instructions](../docker/README.md) to build and run dockerized Tinode chat server up to an including _step 3_.

2. In _step 4_ run the server adding `--env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true` and `--volume botdata:/botdata` to the command line:
	1. **RethinkDB**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true --volume botdata:/botdata --network tinode-net tinode/tinode-rethink:latest
	```
	2. **MySQL**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true --volume botdata:/botdata --network tinode-net tinode/tinode-mysql:latest
	```

	You may replace `:latest` with a different tag. See all all available tags here:
	 * [MySQL tags](https://hub.docker.com/r/tinode/tinode-mysql/tags/)
	 * [RethinkDB tags](https://hub.docker.com/r/tinode/tinode-rethink/tags/)

3. Run the chatbot
	```
	$ docker run -d --name tino-chatbot --network tinode-net --volume botdata:/botdata tinode/chatbot:latest
	```

4. Test that the bot is functional by pointing your browser to http://localhost:6060/, login and talk to user `Tino`. The user should respond to every message with a random quote.


### Using PIP

#### Prerequisites

[gRPC](https://grpc.io/) requires [python](https://www.python.org/) 2.7 or 3.4 or higher.
Make sure [pip](https://pip.pypa.io/en/stable/installing/) 9.0.1 or higher is installed.
```
$ python -m pip install --upgrade pip
```
If you cannot upgrade pip due to a system-owned installation, you can run install it in a `virtualenv`:
```
$ python -m pip install virtualenv
$ virtualenv venv
$ source venv/bin/activate
$ python -m pip install --upgrade pip
```

If you are using python 2.7 install `futures`:
```
$ python -m pip install futures
```

#### Install tinode-grpc

Install tinode gRPC bindings:
```
$ python -m pip install tinode-grpc
```

Or, to install it system wide:
```
$ sudo python -m pip install tinode-grpc
```

On El Capitan OSX, you may get the following error:
```
$ OSError: [Errno 1] Operation not permitted: '/tmp/pip-qwTLbI-uninstall/System/Library/Frameworks/Python.framework/Versions/2.7/Extras/lib/python/six-1.4.1-py2.7.egg-info'
```
You can work around this using:
```
$ python -m pip install tinode-grpc --ignore-installed
```

#### Run the chatbot

Start the [tinode server](../INSTALL.md) first. Then start the chatbot with credentials of the user you want to be your bot, `alice` in this example:
```
python chatbot.py --login-basic=alice:alice123
```
If you want to run the bot in the background, start it as
```
nohup python chatbot.py --login-basic=alice:alice123 &
```
Run `python chatbot.py -h` for more options.

If you are using python 2.7, keep in mind that `condition.wait()` [is forever buggy](https://bugs.python.org/issue8844). As a consequence of this bug the bot cannot be terminated with a SIGINT. It has to be stopped with a SIGKILL.  

You can use cookie file to store credentials. Sample cookie files are provided as `basic-cookie.sample` and `token-cookie.sample`. Once authenticated the bot will attempt to store the token in the cookie file, `.tn-cookie` by default. If you have a cookie file with the default name, you can run the bot with no parameters:
```
python chatbot.py
```

Quotes are read from `quotes.txt` by default. The file is plain text with one quote per line.
