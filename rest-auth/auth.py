from flask import Flask, jsonify

app = Flask(__name__)

@app.route('/')
def index():
    return 'Sample REST authentication service. '+\
        'See <a href="https://github.com/tinode/chat/rest-auth/">https://github.com/tinode/chat/rest-auth/</a> for details.'

@app.route('/add')
def add():
    return jsonify({"err":"unsupported"})

@app.route('/auth')
def auth():
    return ""

@app.route('/checkunique')
def checkunique():
    return jsonify({"err":"unsupported"})

@app.route('/del')
def xdel():
    return jsonify({"err":"unsupported"})

@app.route('/gen')
def gen():
    return jsonify({"err":"unsupported"})

@app.route('/link')
def link():
    return ""

@app.route('/upd')
def upd():
    return jsonify({"err":"unsupported"})

if __name__ == '__main__':
    app.run(debug=True)
