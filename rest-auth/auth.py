from flask import Flask, jsonify, make_response

app = Flask(__name__)

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

    return ""

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

    # TODO: save the link account <-> secret to database.

    # Success
    return jsonify({})

@app.route('/upd', methods=['POST'])
def upd():
    return jsonify({'err': 'unsupported'})

@app.errorhandler(404)
def not_found(error):
    return make_response(jsonify({'err': 'not found'}), 404)

if __name__ == '__main__':
    app.run(debug=True)
