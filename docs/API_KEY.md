# How to add new/own API key to tinode?

## What is API key?

These keys are used to generate an API key which provides some minor protection from automated tools scraping the API. For this purpose, two keys are used:

* `HMAC salt` that is used on the server side to verify the API key.
* `API key` that is used on the client side.

## Generate Keys Automatically

Use the [keygen](https://github.com/tinode/chat/blob/master/keygen/README.md) which is available in the tinode folder. Run this program with the `-salt auto` option. As below:

```bash
./keygen -salt auto
```

result like:

```text
API key v1 seq1 [ordinary]: AQAAAAABAACGOIyP2vh5avSff5oVvMpk
Used HMAC salt: TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```

There are two lines:

* In first line detect _API key_. In this example is `AQAAAAABAACGOIyP2vh5avSff5oVvMpk`.
* In second line detect _HMAC salt_. In this example is `TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=`.

*Note*: This value is random and will be different for you.

## Add `HMAC salt` to Tinode

Edit file [tinode.conf](https://github.com/tinode/chat/blob/master/server/tinode.conf) in the tinode folder. Find `api_key_salt` and change value it. Like:

```json
....
	// Salt for signing API key. 32 random bytes base64-encoded. Use 'keygen' tool (included in this
	// distro) to generate the API key and the salt.
	"api_key_salt": "TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=",
....

```

Then restart tinode.

## Add `API key` to Tinode

### Add to WebApp

Open file `index.*.js` in the _tinode/static/umd_ folder. Find `API_KEY` variable and change it. Like:

```js
....
const API_KEY = 'AQAAAAAFAAAqXLPhti0JwhyVXBWKXTPW';
....

```

*Note*: The preferred way is to edit the source file then recompile.

