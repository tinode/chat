# REST authenticator

This authenticator permits authentication of Tinode users and creation of Tinode accounts using a separate process as a source of truth.

This authenticator calls a REST service over HTTP(S). A skeleton implementation of such a server is provided for reference at [rest-auth](../../../rest-auth/).

Request and response payloads are formatted as JSON. Some of the request or response fields may be skipped.

## Request

```js
{
  "endpoint": "auth", // string, one of the endpoints as described below.
  "secret": "",       // authentication secret as provided by the client.
  "rec": {            // authentication record
    {
      "uid": "LELEQHDWbgY", // user ID, int64 base64-encoded
      "authlvl": "auth",    // authentication level
      "lifetime": "10000s", // lifetime of this record
                            // see https://golang.org/pkg/time/#Duration for format.
    	"features": 2,        // bitmap of features
    	"tags": ["email:alice@example.com"] // Tags associated with this authentication record.
    }
  }
}
```

## Response

```js
{
  "err": "internal", // string, error message in case of an error.
  "rec": {           // authentication record.
    ...              // the same as `request.rec`
  },
  "byteval": "",     // array of bytes, optional
  "ts": "",          // time stamp, optional
  "boolval": true,   // boolean value, optional
  "newacc": {        // data to use for creating a new account.
    // Default access mode
    "auth": "JRWPS",
    "anon": "N"
  	"public": {...}  // user's public data
  	"private": {...} // user's private data
  }
}
```

The server must implement the following endpoints:

## `add` Add new authentication record

This endpoint requests server to add a new authentication record. This endpoint is generally used for account creation. If accounts are managed externally, it's likely to be unused and should generally return an error `"unsupported"`:
```json
{ "err": "unsupported" }
```

#### Sample request:
```json
{
  "endpoint": "add",
  "secret": "Ym9iOmJvYjEyMw==",
  "rec": {
    "uid": "LELEQHDWbgY",
    "lifetime": "10000s",
    "features": 2,
    "tags": ["email:alice@example.com"]
  }
}
```

#### Sample response (rec values may change):
```json
{
  "rec": {
    "uid": "LELEQHDWbgY",
    "authlvl": "auth",
    "lifetime": "5000s",
    "features": 1,
    "tags": ["email:alice@example.com", "uname:alice"]
  }
}
```

## `auth` Request for authentication

## `checkunique` Checks if provided authentication record is unique.

## `del` Requests to delete authentication record.

## `gen` Generate authentication secret.

## `link` Requests server to link new account ID to authentication record.

## `upd` Update authentication record.
