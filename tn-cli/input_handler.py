"""User input handling for tn-cli."""

from __future__ import print_function

import sys

import tn_globals
from tn_globals import printerr


# Prints prompt and reads lines from stdin.
def readLinesFromStdin():
    if tn_globals.IsInteractive:
        while True:
            try:
                line = tn_globals.Prompt.prompt()
                yield line
            except EOFError as e:
                # Ctrl+D.
                break
    else:
        # iter(...) is a workaround for a python2 bug https://bugs.python.org/issue3907
        for cmd in iter(sys.stdin.readline, ''):
            yield cmd


# Stdin reads a possibly multiline input from stdin and queues it for asynchronous processing.
def stdin(InputQueue):
    partial_input = ""
    try:
        for cmd in readLinesFromStdin():
            cmd = cmd.strip()
            # Check for continuation symbol \ in the end of the line.
            if len(cmd) > 0 and cmd[-1] == "\\":
                cmd = cmd[:-1].rstrip()
                if cmd:
                    if partial_input:
                        partial_input += " " + cmd
                    else:
                        partial_input = cmd

                if tn_globals.IsInteractive:
                    sys.stdout.write("... ")
                    sys.stdout.flush()

                continue

            # Check if we have cached input from a previous multiline command.
            if partial_input:
                if cmd:
                    partial_input += " " + cmd
                InputQueue.append(partial_input)
                partial_input = ""
                continue

            InputQueue.append(cmd)

            # Stop processing input
            if cmd == 'exit' or cmd == 'quit' or cmd == '.exit' or cmd == '.quit':
                return

    except Exception as ex:
        printerr("Exception in stdin", ex)

    InputQueue.append('exit')
