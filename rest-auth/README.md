# Example of a REST authenticator server.

This is an example of a server-side [REST authenticator](../server/auth/rest/). It's a basic Python script meant to be run as a web server. It implements the required endpoints. It responds to all requests with dummy data.

The service uses [Flask](http://flask.pocoo.org/), so make sure it's installed:
```
pip install flask
```
Run the service as
```
python auth.py
```
