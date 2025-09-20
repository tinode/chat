# keygen: API key generator

A command-line utility to generate an API key for [Tinode server](../server/)

**Parameters:**

 * `sequence`: Sequential number of the API key. This value can be used to reject previously issued keys.
 * `isroot`: Currently unused. Intended to designate key of a system administrator.
 * `validate`: Key to validate: check previously issued key for validity.
 * `salt`: [HMAC](https://en.wikipedia.org/wiki/HMAC) salt, 32 random bytes base64 standard encoded; must be present for key validation; optional when generating the key: if missing, a cryptographically-strong salt will be automatically generated.

**Encryption Key Generation:**

 * `encryption`: Generate encryption key instead of API key.
 * `keysize`: Encryption key size in bytes (default: 32 for AES-256).
 * `output`: Output file for the encryption key (optional).


## Usage

The API key is used to provide some protection from automatic scraping of server API and for identification of client applications.

* `API key` is used on the client side.
* `HMAC salt` is used on the server side to verify the API key.

Run the generator:

```sh
./keygen
```

Sample output:

```text
API key v1 seq1 [ordinary]: AQAAAAABAACGOIyP2vh5avSff5oVvMpk
HMAC salt: TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```

**Generate Encryption Key:**

```sh
# Generate 32-byte encryption key
./keygen -encryption

# Generate custom size key
./keygen -encryption -keysize 32

# Save key to file
./keygen -encryption -output encryption.key
```

Sample encryption key output:

```text
Generated 32-byte encryption key:
dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdGtleXRlc3Q=

Add this to your tinode.conf:
"encryption": {
    "enabled": true,
    "key": "dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdGtleXRlc3Q="
}
```

Copy `HMAC salt` to `api_key_salt` parameter in your server [config file](https://github.com/tinode/chat/blob/master/server/tinode.conf).
Copy `API key` to the client applications:

 * TinodeWeb: `API_KEY` in [config.js](https://github.com/tinode/webapp/blob/master/src/config.js)
 * Tindroid: `API_KEY` in [Cache.java](https://github.com/tinode/tindroid/blob/master/app/src/main/java/co/tinode/tindroid/Cache.java)
 * Tinodious: `kApiKey` in [SharedUtils.swift](https://github.com/tinode/ios/blob/master/TinodiosDB/SharedUtils.swift)

Rebuild the clients after changing the API key.
