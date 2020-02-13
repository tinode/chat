"""Global objects for Tinode command line client."""
# To make print() compatible between p2 and p3
from __future__ import print_function

import sys
from collections import deque
try:
    import Queue as queue
except ImportError:
    import queue

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
