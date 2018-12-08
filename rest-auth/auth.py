from flask import Flask, jsonify, make_response
import base64
import json

dummy_data = {}

app = Flask(__name__)

def parse_secret(ecoded_secret):
    secret = base64.b64decode(ecoded_secret)
    return secret.split(':')

@app.route('/')
def index():
    return 'Sample Tinode REST authentication service. '+\
        'See <a href="https://github.com/tinode/chat/rest-auth/">https://github.com/tinode/chat/rest-auth/</a> for details.'

@app.route('/add', methods=['POST'])
def add():
    return jsonify({'err': 'unsupported'})

@app.route('/auth', methods=['POST'])
def auth():
    if not request.json:
        return jsonify({'err': 'malformed'})
    uname, password = parse_secret(request.json.secret)
    if dummy_data[uname]:
        if dummy_data[uname]['password'] != password:
            # Wrong password
            return jsonify({'err': 'failed'})
        if dummy_data[uname]['uid']:
            # We have uname -> uid mapping
            jsonify({
                'rec': {
                    'uid': dummy_data[uname]['uid'],
                    'authlvl': dummy_data[uname]['authlvl'],
                }
            })
        else:
            # This is the first login. Tell Tinode to create a new account.
            jsonify({
                'rec': {
                    'authlvl': dummy_data[uname]['authlvl'],
                    'tags': dummy_data[uname]['tags']
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
    if not dummy_data[uname]:
        # Unknown user name
        return jsonify({'err': 'not found'})
    if dummy_data[uname]['uid']:
        # Already linked
        return jsonify({'err': 'duplicate value'})

    # Save updated data to file
    dummy_data[uname]['uid'] = rec['uid']
    with open('dummy_data.json', 'w') as outfile:
        json.dump(dummy_data, outfile)

    # Success
    return jsonify({})

@app.route('/upd', methods=['POST'])
def upd():
    return jsonify({'err': 'unsupported'})

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
