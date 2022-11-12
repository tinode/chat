# How to add new/own API key to tinode?

## What is API key?

Trinode encrypts data for security. For this purpose, two keys are used:

* `HMAC salt` in server side. Hereafter referred to as _Server Key_.
* `API key` in client side. Hereafter referred to as _Client Key_.

## Generate Server/Client Keys

### 1) Generate Automatically

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

* In first line detect _Client Key_. In this example is `AQAAAAABAACGOIyP2vh5avSff5oVvMpk`.
* In second line detect _Server Key_. In this example is `TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=`.

*Note*: This value is random and will be different for you.
*Note*: You can use the `-sequence` option to make the key more complex. like:

```bash
./keygen -salt TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw= -sequence 5
```

result is:

```text
API key v1 seq5 [ordinary]: AQAAAAAFAAAqXLPhti0JwhyVXBWKXTPW
Used HMAC salt: TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```


### 2) Generate Manually

#### Prerequisite

For your API key, you need 32 bytes encoded in base64. You can generate this randomly.

##### Generate in Linux

In *Linux*, you can use the following command:

```bash
echo $(head -c 32 /dev/urandom | base64 | tr -d '\n')
```

result like:

```text
TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```

*Note*: This value is random and will be different for you.

##### Generate in Windows

In *Windows*, you can use the following command in `powershell`:

```powershell
Add-Type -AssemblyName System.Security

[Reflection.Assembly]::LoadWithPartialName("System.Security")
$rijndael = new-Object System.Security.Cryptography.RijndaelManaged
$rijndael.GenerateKey()
Write-Host([Convert]::ToBase64String($rijndael.Key))
$rijndael.Dispose()
```

result like:

```text
5vNFwIfHfLKHG+kgWRduWoZS2LW7BcmHXSGJmUaMstQ=
```

*Note*: This value is random and will be different for you.

#### Build Server/Client Keys from your key

Now you need to use the key created in the previous step to create the two keys needed by tinode. Use the [keygen](https://github.com/tinode/chat/blob/master/keygen/README.md) which is available in the tinode folder. Run this program with the `-salt` option and give it the key. As below:

```bash
./keygen -salt TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```

result like:

```text
API key v1 seq1 [ordinary]: AQAAAAABAACGOIyP2vh5avSff5oVvMpk
Used HMAC salt: TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```

There are two lines:

* In first line detect _Client Key_. In this example is `AQAAAAABAACGOIyP2vh5avSff5oVvMpk`.
* In second line detect _Server Key_. In this example is `TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=`.

*Note*: You can use the `-sequence` option to make the key more complex. like:

```bash
./keygen -salt TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw= -sequence 5
```

result is:

```text
API key v1 seq5 [ordinary]: AQAAAAAFAAAqXLPhti0JwhyVXBWKXTPW
Used HMAC salt: TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=
```

## Add Server Key to Tinode

Edit file [tinode.conf](https://github.com/tinode/chat/blob/master/server/tinode.conf) in the tinode folder. Find `api_key_salt` and change value it. Like:

```json
....
	// Salt for signing API key. 32 random bytes base64-encoded. Use 'keygen' tool (included in this
	// distro) to generate the API key and the salt.
	"api_key_salt": "TC0Jzr8f28kAspXrb4UYccJUJ63b7CSA16n1qMxxGpw=",
....

```

Then restart tinode.

## Add Client Key to Tinode

### Add to WebApp

Open file `index.*.js` in the _tinode/static/umd_ folder. Find `API_KEY` variable and change it. Like:

```js
....
const API_KEY = 'AQAAAAAFAAAqXLPhti0JwhyVXBWKXTPW';
....

```

