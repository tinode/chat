# keygen: API key generator

A command-line utility to generate an API key for [Tinode server](../server/)

Parameters:

 * `sequence`: Sequential number of the API key. This value can be used to reject previously issued keys.
 * `isroot`: Currently unused. Intended to designate key of a system administrator.
 * `validate`: Key to validate: check previously issued key for validity.
 * `salt`: [HMAC](https://en.wikipedia.org/wiki/HMAC) salt, 32 random bytes base64 encoded or `auto` to automatically generate salt.
