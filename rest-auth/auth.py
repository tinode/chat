#!/usr/bin/python

# Sample Tinode REST/JSON-RPC authentication service.
# See https://github.com/tinode/chat/rest-auth for details.

from flask import Flask, jsonify, make_response, request
import base64
import json

dummy_data = {}

app = Flask(__name__)

def parse_secret(ecoded_secret):
    secret = base64.b64decode(ecoded_secret)
    return secret.split(':')

@app.route('/')
def index():
    return 'Sample Tinode REST/JSON-RPC authentication service. '+\
        'See <a href="https://github.com/tinode/chat/rest-auth/">https://github.com/tinode/chat/rest-auth/</a> for details.'

@app.route('/add', methods=['POST'])
def add():
    return jsonify({'err': 'unsupported'})

@app.route('/auth', methods=['POST'])
def auth():
    if not request.json:
        return jsonify({'err': 'malformed'})
    uname, password = parse_secret(request.json.get('secret'))
    if uname in dummy_data:
        if dummy_data[uname]['password'] != password:
            # Wrong password
            return jsonify({'err': 'failed'})
        if 'uid' in dummy_data[uname]:
            # We have uname -> uid mapping
            return jsonify({
                'rec': {
                    'uid': dummy_data[uname]['uid'],
                    'authlvl': dummy_data[uname]['authlvl'],
                    'features': dummy_data[uname]['features']
                }
            })
        else:
            # This is the first login. Tell Tinode to create a new account.
            return jsonify({
                'rec': {
                    'authlvl': dummy_data[uname]['authlvl'],
                    'tags': dummy_data[uname]['tags'],
                    'features': dummy_data[uname]['features']
                },
                'newacc': {
                    'auth': dummy_data[uname]['auth'],
                    'anon': dummy_data[uname]['anon'],
                    'public': dummy_data[uname]['public'],
                    'private': dummy_data[uname]['private']
                }
            })
        return jsonify({'err': 'unsupported'})
    else:
        return jsonify({'err': 'not found'})

@app.route('/checkunique', methods=['POST'])
def checkunique():
    return jsonify({'err': 'unsupported'})

@app.route('/del', methods=['POST'])
def xdel():
    return jsonify({'err': 'unsupported'})

@app.route('/gen', methods=['POST'])
def gen():
    return jsonify({'err': 'unsupported'})

@app.route('/link', methods=['POST'])
def link():
    if not request.json:
        return jsonify({'err': 'malformed'})

    rec = request.json.get('rec', None)
    secret = request.json.get('secret', '')
    if not rec or not rec['uid'] or not secret:
        return jsonify({'err': 'malformed'})

    # Save the link account <-> secret to database.
    uname, password = parse_secret(secret)
    if uname not in dummy_data:
        # Unknown user name
        return jsonify({'err': 'not found'})
    if 'uid' in dummy_data[uname]:
        # Already linked
        return jsonify({'err': 'duplicate value'})

    # Save updated data to file
    dummy_data[uname]['uid'] = rec['uid']
    with open('dummy_data.json', 'w') as outfile:
        json.dump(dummy_data, outfile, indent=2, sort_keys=True)

    # Success
    return jsonify({})

@app.route('/upd', methods=['POST'])
def upd():
    return jsonify({'err': 'unsupported'})

@app.route('/rtagns', methods=['POST'])
def rtags():
    # Return dummy namespace "rest" and "email", let client check logins by regular expression.
    return jsonify({'strarr': ['rest', 'email'], 'byteval': base64.b64encode('^[a-z0-9_]{3,8}$')})

@app.errorhandler(404)
def not_found(error):
    return make_response(jsonify({'err': 'not found'}), 404)

@app.errorhandler(405)
def not_found(error):
    return make_response(jsonify({'err': 'method not allowed'}), 405)

if __name__ == '__main__':
    # Load previously saved dummy data. Dummy data contains
    # tinode user id <-> user name mapping and data for account creation.
    with open('dummy_data.json') as infile:
        dummy_data = json.load(infile)
    app.run(debug=True)
