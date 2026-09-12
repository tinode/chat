import base64
import sys
import unittest
from pathlib import Path
from unittest.mock import mock_open, patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import auth


class AuthTestCase(unittest.TestCase):
    def setUp(self):
        self.original_dummy_data = auth.dummy_data
        self.original_testing = auth.app.config.get('TESTING', False)
        auth.app.config['TESTING'] = True
        auth.dummy_data = {
            'alice': {
                'anon': 'N',
                'auth': 'JRWPA',
                'authlvl': 'auth',
                'features': 'V',
                'password': 'alice123',
                'private': '',
                'public': {},
                'tags': ['email:alice@example.com'],
            }
        }
        self.client = auth.app.test_client()

    def tearDown(self):
        auth.dummy_data = self.original_dummy_data
        auth.app.config['TESTING'] = self.original_testing

    @staticmethod
    def encode_secret(username, password):
        secret = f'{username}:{password}'.encode('utf-8')
        return base64.b64encode(secret).decode('ascii')

    def test_parse_secret_preserves_colons_in_password(self):
        encoded_secret = self.encode_secret('alice', 'part:two')

        self.assertEqual(
            auth.parse_secret(encoded_secret),
            ('alice', 'part:two'))

    def test_auth_returns_existing_account(self):
        auth.dummy_data['alice']['uid'] = 'usrAlice'

        response = self.client.post(
            '/auth',
            json={'secret': self.encode_secret('alice', 'alice123')})

        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.get_json(), {
            'rec': {
                'uid': 'usrAlice',
                'authlvl': 'auth',
                'features': 'V',
            }
        })

    def test_malformed_requests_return_api_errors(self):
        cases = [
            ('/auth', None),
            ('/auth', []),
            ('/auth', {'secret': 'not-base64'}),
            ('/auth', {'secret': None}),
            ('/link', {
                'rec': {},
                'secret': self.encode_secret('alice', 'alice123'),
            }),
        ]

        for endpoint, payload in cases:
            if payload is None:
                response = self.client.post(endpoint)
            else:
                response = self.client.post(endpoint, json=payload)

            with self.subTest(endpoint=endpoint, payload=payload):
                self.assertEqual(response.status_code, 200)
                self.assertEqual(response.get_json(), {'err': 'malformed'})

    def test_rtagns_returns_base64_regex_text(self):
        response = self.client.post('/rtagns', json={})

        self.assertEqual(response.status_code, 200)
        result = response.get_json()
        self.assertEqual(result['strarr'], ['rest', 'email'])
        self.assertEqual(
            base64.b64decode(result['byteval'], validate=True),
            b'^[a-z0-9_]{3,8}$')

    def test_link_rejects_wrong_password_without_mutation(self):
        response = self.client.post('/link', json={
            'rec': {'uid': 'usrWrong'},
            'secret': self.encode_secret('alice', 'wrong'),
        })

        self.assertEqual(response.get_json(), {'err': 'failed'})
        self.assertNotIn('uid', auth.dummy_data['alice'])

    def test_link_persists_uid_after_valid_password(self):
        with patch('builtins.open', mock_open()) as open_file:
            response = self.client.post('/link', json={
                'rec': {'uid': 'usrAlice'},
                'secret': self.encode_secret('alice', 'alice123'),
            })

        self.assertEqual(response.get_json(), {})
        self.assertEqual(auth.dummy_data['alice']['uid'], 'usrAlice')
        open_file.assert_called_once_with('dummy_data.json', 'w')


if __name__ == '__main__':
    unittest.main()
