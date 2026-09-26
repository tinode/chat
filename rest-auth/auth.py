#!/usr/bin/python

# Sample Tinode REST/JSON-RPC authentication service.
# See https://github.com/tinode/chat/rest-auth for details.

from flask import Flask, jsonify, make_response, request
import base64
import binascii
import json

dummy_data = {}

app = Flask(__name__)

def parse_secret(encoded_secret):
    try:
        # Tinode sends the secret as base64 JSON text; normalize it to a string
        # here so authentication data can be compared with dummy_data values.
        secret = base64.b64decode(encoded_secret, validate=True).decode('utf-8')
    except (binascii.Error, TypeError, UnicodeError, ValueError):
        raise ValueError('malformed secret') from None

    # Only the first colon separates the username from the password.
    uname, separator, password = secret.partition(':')
    if not separator or not uname:
        raise ValueError('malformed secret')
    return uname, password

@app.route('/')
def index():
    return 'Sample Tinode REST/JSON-RPC authentication service. '+\
        'See <a href="https://github.com/tinode/chat/rest-auth/">https://github.com/tinode/chat/rest-auth/</a> for details.'

@app.route('/add', methods=['POST'])
def add():
    return jsonify({'err': 'unsupported'})

@app.route('/auth', methods=['POST'])
def auth():
    # Use silent parsing so invalid JSON or a wrong content type gets the API
    # error response instead of Flask's default HTML error page.
    payload = request.get_json(silent=True)
    if not isinstance(payload, dict):
        return jsonify({'err': 'malformed'})
    try:
        uname, password = parse_secret(payload.get('secret'))
    except ValueError:
        return jsonify({'err': 'malformed'})
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
    # Validate the object before indexing into it so malformed requests remain
    # client errors rather than becoming server exceptions.
    payload = request.get_json(silent=True)
    if not isinstance(payload, dict):
        return jsonify({'err': 'malformed'})

    rec = payload.get('rec')
    secret = payload.get('secret')
    if (not isinstance(rec, dict) or not isinstance(rec.get('uid'), str)
            or not rec['uid'] or not isinstance(secret, str) or not secret):
        return jsonify({'err': 'malformed'})

    # Recheck the secret because this endpoint changes the persistent UID link.
    try:
        uname, password = parse_secret(secret)
    except ValueError:
        return jsonify({'err': 'malformed'})
    if uname not in dummy_data:
        # Unknown user name
        return jsonify({'err': 'not found'})
    if dummy_data[uname]['password'] != password:
        return jsonify({'err': 'failed'})
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
    # JSON has no byte type, so encode the regex as the contract's base64 text.
    byteval = base64.b64encode(b'^[a-z0-9_]{3,8}$').decode('ascii')
    return jsonify({'strarr': ['rest', 'email'], 'byteval': byteval})

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
