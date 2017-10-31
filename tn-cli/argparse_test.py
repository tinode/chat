import argparse

class JoinArgs(argparse.Action):
    def __call__(self, parser, namespace, values, option_string=None):
        setattr(namespace, self.dest, ' '.join(values))

def parse_command(cmd):
    """Parses command line input into a ClientMsg"""
    parts = cmd.split(" ")
    parser = argparse.ArgumentParser(prog=parts[0])
    if parts[0] == "acc":
        parser.add_argument('--user', default=None, help='user ID to change')
        parser.add_argument('--secret', default=None, help='secret for authentication')
        parser.add_argument('--scheme', default="basic", help='authentication scheme')
        parser.add_argument('--uname', default=None, help='user name for basic authentication')
        parser.add_argument('--password', default=None, help='password for basic authentication')
        parser.add_argument('--do-login', action='store_true', help='login with the newly created account')
        parser.add_argument('--tags', action=None, help='tags for user discovery, e.g. "--tags=email:abc@example.com,tel:1234567890"')
        parser.add_argument('--desc', default=None, help='user parameters')
    elif parts[0] == "login":
        parser.add_argument('--scheme', default="basic")
        parser.add_argument('secret', nargs='?', default=argparse.SUPPRESS)
        parser.add_argument('--secret', dest='secret', default=None)
        parser.add_argument('--uname', default=None)
        parser.add_argument('--password', default=None)
    elif parts[0] == "sub":
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to subscribe to')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to subscribe to')
    elif parts[0] == "leave":
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS, help='topic to detach from')
        parser.add_argument('--topic', dest='topic', default=None, help='topic to detach from')
        parser.add_argument('--unsub', action='store_true', help='detach and unsubscribe from topic')
    elif parts[0] == "pub":
        parser.add_argument('topic', nargs='?', default=argparse.SUPPRESS)
        parser.add_argument('--topic', dest='topic', default=None)
        parser.add_argument('content', nargs='*', default=argparse.SUPPRESS, action=JoinArgs, help='message to send')
        parser.add_argument('--content', nargs='*', dest='content', action=JoinArgs, help='message to send')
    elif parts[0] == "get":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "set":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "del":
        print("Not implemented: " + parts[0])
        return None
    elif parts[0] == "note":
        print("Not implemented: " + parts[0])
        return None
    else:
        print "Unrecognized:", parts[0]
        print "Possible commands:"
        print "\tacc, login, sub, leave, pub, get, set, del, note"
        print "\n\tType <command> -h for help"
        return None

    try:
        return parser.parse_args(parts[1:])
    except SystemExit:
        return None

def run():
    while True:
        inp = raw_input("tn> ")
        if inp == "":
            continue
        if inp == "exit" or inp == "quit":
            return
        cmd = parse_command(inp)
        if cmd != None:
            print cmd

if __name__ == '__main__':
    run()
