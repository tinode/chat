# TNPG: Push Gateway

This is push notifications adapter which communicates with Tinode Push Gateway (TNPG).

TNPG is a proprietary service intended to simplify deployment of on-premise installations.
Deploying a Tinode server without TNPG requires [configuring Google FCM](../fcm/) with your own credentials, recompiling Android and iOS clients, releasing them to PlayStore and AppStore under your own accounts. It's usually time consuming and relatively complex.

TNPG solves this problem by allowing you to send push notifications on behalf of Tinode: you hand a notification over to Tinode, Tinode sends it to  client using its own credentials and certificates. Internally it uses [Google FCM](https://firebase.google.com/docs/cloud-messaging/) and as such supports the same platforms as FCM. The main advantage of using TNPG over FCM is simplicity of configuration: mobile clients don't have to be recompiled, all is needed is a configuration update on the server.

## Configuring TNPG adapter

### Obtain TNPG token

1. Register at https://console.tinode.co and create an organization.
2. Get the TPNG token from the _Self hosting_ &rarr; _Push Gateway_ section by following the instructions there.

### Configure the server
Update the server config [`tinode.conf`](../../tinode.conf#L413), section `"push"` -> `"name": "tnpg"`:
```js
{
  "enabled": true,
  "org": "myorg", // Short name (URL) of the organization you registered at console.tinode.co
  "token": "SoMe_LonG.RaNDoM-StRiNg.12345" // authentication token obtained from console.tinode.co
}
```
Make sure the `fcm` section is disabled `"enabled": false` or removed altogether.
