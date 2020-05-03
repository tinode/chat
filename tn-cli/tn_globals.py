"""Global objects for Tinode command line client."""
# To make print() compatible between p2 and p3
from __future__ import print_function

import json
import sys
from collections import deque
from google.protobuf.json_format import MessageToDict
try:
    import Queue as queue
except ImportError:
    import queue

if sys.version_info[0] >= 3:
    # for compatibility with python2
    unicode = str

# Dictionary wich contains lambdas to be executed when server {ctrl} response is received.
OnCompletion = {}

# Outstanding request for a synchronous message.
WaitingFor = None

# Last obtained authentication token
AuthToken = ''

# IO queues and a thread for asynchronous input/output
#InputQueue = queue.Queue()
InputQueue = deque()
OutputQueue = queue.Queue()
InputThread = None

# Detect if the tn-cli is running interactively or being piped.
IsInteractive = sys.stdin.isatty()
Prompt = None

# Default values for user and topic
DefaultUser = None
DefaultTopic = None

# Variables: results of command execution
Variables = {}

# Flag to enable extended logging. Useful for debugging.
Verbose = False

# Print prompts in interactive mode only.
def printout(*args):
    if IsInteractive:
        print(*args)

def printerr(*args):
    text = ""
    for a in args:
        text = text + str(a) + " "
    # Strip just the spaces here, don't strip the newline or tabs.
    text = text.strip(" ")
    if text:
        sys.stderr.write(text + "\n")

# Support for asynchronous input-output to/from stdin/stdout

# Stdout asynchronously writes to sys.stdout
def stdout(*args):
    text = ""
    for a in args:
        text = text + str(a) + " "
    # Strip just the spaces here, don't strip the newline or tabs.
    text = text.strip(" ")
    if text:
        OutputQueue.put(text)

# Stdoutln asynchronously writes to sys.stdout and adds a new line to input.
def stdoutln(*args):
    args = args + ("\n",)
    stdout(*args)

# Shorten long strings for logging.
def clip_long_string(obj):
    if isinstance(obj, str) or isinstance(obj, unicode):
        if len(obj) > 64:
            return '<' + str(len(obj)) + ' bytes: ' + obj[:12] + '...' + obj[-12:] + '>'
        return obj
    elif isinstance(obj, (list, tuple)):
        return [clip_long_string(item) for item in obj]
    elif isinstance(obj, dict):
        return dict((key, clip_long_string(val)) for key, val in obj.items())
    else:
        return obj

# Convert protobuff message to json. Shorten very long strings.
def to_json(msg):
    if not msg:
        return 'null'
    try:
        return json.dumps(clip_long_string(MessageToDict(msg)))
    except Exception as err:
        stdoutln("Exception: {}".format(err))

    return 'exception'
