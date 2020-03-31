# FCM push adapter

This adapter sends push notifications to mobile clients and web browsers using [Google FCM](https://firebase.google.com/docs/cloud-messaging/). As of the time of this writing it supports Android with [Play Services](https://developers.google.com/android/guides/overview), iOS devices, and all major web browsers [excluding Safari](https://caniuse.com/#feat=push-api).

This adapter requires you to obtain your own credentials from Goole Firebase. If you want to use iOS and Android mobile apps with your service, they must be recompiled with your credentials obtained from Google. If you do not want to recompile mobile clients, consider using TNPG adapter instead.


## Configuring FCM adapter

### Server and TinodeWeb

1. Create a project at https://firebase.google.com/ if you have not done so already.
2. Follow instructions at https://cloud.google.com/iam/docs/creating-managing-service-account-keys to download the credentials file.
3. Update the server config [`tinode.conf`](../server/tinode.conf#L255), section `"push"` -> `"name": "fcm"`. Do _ONE_ of the following:
  * _Either_ enter the path to the downloaded credentials file into `"credentials_file"`.
  * _OR_ copy the file contents to `"credentials"`.<br/><br/>
    Remove the other entry. I.e. if you have updated `"credentials_file"`, remove `"credentials"` and vice versa.
4. Update [TinodeWeb](/tinode/webapp/) config [`firebase-init.js`](https://github.com/tinode/webapp/blob/master/firebase-init.js): update `apiKey`, `messagingSenderId`, `projectId`, `appId`, `messagingVapidKey`. See more info at https://github.com/tinode/webapp/#push_notifications

### iOS and Android

1. If you are using an Android client, add `google-services.json` to [Tindroid](/tinode/tindroid/) by following instructions at https://developers.google.com/android/guides/google-services-plugin and recompile the client. You may also optionally submit it to Google Play Store.
See more info at https://github.com/tinode/tindroid/#push_notifications
2. If you are using an iOS client, add `GoogleService-Info.plist` to [Tinodios](/tinode/ios/) by following instructions at https://firebase.google.com/docs/cloud-messaging/ios/client) and recompile the client. You may optionally submit the app to Apple AppStore.
See more info at https://github.com/tinode/ios/#push_notifications
