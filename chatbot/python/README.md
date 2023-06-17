# Tinode Chatbot

This is a simple chatbot for Tinode using [gRPC API](../../pbx/). It's written in Python as a demonstration
that the API is language-independent.

The chat bot subscribes to events stream using Plugin API and logs in to Tinode server as a regular user over gRPC interface (see `grpc_listen` in [tinode.conf](../../server/tinode.conf) file). The event stream API is used to listen for creation of new accounts. When a new account is created, the bot initiates a p2p topic with the new user. Then it listens for messages sent to the topic and responds to each with a random quote from `quotes.txt` file.

Generated files are provided for convenience in a [separate folder](../../py_grpc/tinode_grpc). You may re-generate them if needed:
```
python -m pip install grpcio-tools
python -m grpc_tools.protoc -../../pbx --python_out=. --grpc_python_out=. ../../pbx/model.proto
```

Chatbot expects gRPC binding to be provided as `tinode-grpc`. If you want to use them locally, first copy `model_pb2.py` and `model_pb2_grpc.py` to the same folder as `chatbot.py` then find the lines
```
from tinode_grpc import pb
from tinode_grpc import pbx
```
in `chatbot.py` and replace them with
```
import model_pb2 as pb
import model_pb2_grpc as pbx
```

## Installing and running

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

#### Install dependencies:
```
$ python -m pip install -r requirements.txt
```

On El Capitan OSX, you may get the following error:
```
$ OSError: [Errno 1] Operation not permitted: '/tmp/pip-qwTLbI-uninstall/System/Library/Frameworks/Python.framework/Versions/2.7/Extras/lib/python/six-1.4.1-py2.7.egg-info'
```
You can work around this using:
```
$ python -m pip install tinode_grpc --ignore-installed
```

### Run the chatbot

Start the [tinode server](../../INSTALL.md) first. Then start the chatbot with credentials of the user you want to be your bot, `alice` in this example:
```
python chatbot.py --login-basic=alice:alice123
```
If you want to run the bot in the background, start it as
```
nohup python chatbot.py --login-basic=alice:alice123 &
```
Run `python chatbot.py -h` for more options.

If you are using python 2, keep in mind that `condition.wait()` [is forever buggy](https://bugs.python.org/issue8844). As a consequence of this bug the bot cannot be terminated with a SIGINT. It has to be stopped with a SIGKILL.

You can use cookie file to store credentials. Sample cookie files are provided as `basic-cookie.sample` and `token-cookie.sample`. Once authenticated the bot will store the token in the cookie file, `.tn-cookie` by default. If you have a cookie file with the desired credentials, you can run the bot with no parameters:
```
python chatbot.py
```

If the server is configured to use TLS, i.e. running as `httpS://my-server.example.com/`, the gRPC endpoint also uses the same SSL certificate. In that case add the `--ssl` option when starting the chatbot. If you want the chatbot to connect to the secure server over a local network or under a different name rather than the `my-server.example.com`, for instance as `localhost`, you must specify the SSL domain name to use, otherwise the server will not be able to find the right SSL certificate:
```
python chatbot.py --host=localhost:16060 --ssl --ssl-host=my-server.example.com
```

Quotes are read from `quotes.txt` by default. The file is plain text with one quote per line.


### Using Docker

**Warning!** Although the chatbot itself is less than 11KB, the chatbot Docker image is 175MB: the `:slim` Python 3 image is about 140MB, gRPC adds another ~30MB.

1. Follow [instructions](../../docker/README.md) to build and run dockerized Tinode chat server up to and including _step 3_.

2. In _step 4_ run the server adding `--env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true` and `--volume botdata:/botdata` to the command line:
	1. **RethinkDB**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true --volume botdata:/botdata --network tinode-net tinode/tinode-rethink:latest
	```
	2. **MySQL**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true --volume botdata:/botdata --network tinode-net tinode/tinode-mysql:latest
	```
	3. **MongoDB**:
	```
	$ docker run -p 6060:18080 -d --name tinode-srv --env PLUGIN_PYTHON_CHAT_BOT_ENABLED=true --volume botdata:/botdata --network tinode-net tinode/tinode-mongodb:latest
	```

3. Run the chatbot
	```
	$ docker run -d --name tino-chatbot --network tinode-net --volume botdata:/botdata tinode/chatbot:latest
	```

4. Test that the bot is functional by pointing your browser to http://localhost:6060/, login and talk to user `Tino`. The user should respond to every message with a random quote.


You may replace the `:latest` with a different tag. See all available tags here:
 * [Tinode-MySQL tags](https://hub.docker.com/r/tinode/tinode-mysql/tags/)
 * [Tinode-RethinkDB tags](https://hub.docker.com/r/tinode/tinode-rethink/tags/)
 * [Tinode-MongoDB tags](https://hub.docker.com/r/tinode/tinode-mongodb/tags/)
 * [Chatbot tags](https://hub.docker.com/r/tinode/chatbot/tags/)

In general try to use docker images all with the same tag.
