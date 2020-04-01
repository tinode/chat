# TNPG: Push Gateway

This is push notifications adapter which communicates with Tinode Push Gateway (TNPG).

TNPG is a proprietary service intended to simplify deployment of on-premise installations.
Deploying a Tinode server without TNPG requires [configuring Google FCM](../fcm/) with your own credentials including recompiling mobile clients and releasing them to PlayStore and AppStore under your own accounts which is usually time consuming and relatively complex.

TNPG solves this problem by allowing you to send push notifications on behalf of Tinode. Internally it uses Google FCM and as such supports the same platforms as FCM. The main advantage of using TNPG over FCM is simplicity of configuration: mobile clients do not need to be recompiled, all is needed is a configuration update on the server.

## Configuring TNPG adapter

### Obtain TNPG token

1. Register at https://console.tinode.co and create an organization.
2. Get the TPNG token from the _On premise_ section by following the instructions there.

### Configure the server

Update the server config [`tinode.conf`](../server/tinode.conf#L384), section `"push"` -> `"name": "tnpg"`:
```js
{
  "enabled": true,
  "org": "myorg", // name of the organization you registered at console.tinode.co
  "token": "SoMe_LonG.RaNDoM-StRiNg.12345" // authentication token obtained from console.tinode.co
}
```
Make sure the `fcm` section is disabled `"enabled": false`.
