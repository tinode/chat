# Frequently Asked Questions


### Q: I'm running Tinode server in a docker container. Where can I find server logs?<br/>
**A**: The log is in the container at `/var/log/tinode.log`. Attach to a running container with command
```
docker exec -it name-of-the-running-container /bin/bash
```
Then, for instance, see the log with `tail -50 /var/log/tinode.log`

If the container has stopped already, you can copy the log out of the container (saving it to `./tinode.log`):
```
docker cp name-of-the-container:/var/log/tinode.log ./tinode.log
```

Alternatively, you can instruct the docker container to save the logs to a directory on the host by mapping a host directory to `/var/log/` in the container. Add `-v /where/to/save/logs:/var/log` to the `docker run` command.

### Q: How to setup FCM push notifications?<br/>
**A**: If you running the server directly:
1. Create a project at https://console.firebase.google.com if you have not done so already.
2. Follow instructions at https://cloud.google.com/iam/docs/creating-managing-service-account-keys to download the credentials file.
3. Update the server config: `"push"` -> `"name": "fcm"` section of the [`tinode.conf`](https://github.com/tinode/chat/blob/master/server/tinode.conf#L222) file. Do _ONE_ of the following:
  * _Either_ enter the path to the downloaded file into `"credentials_file"`.
  * _OR_ copy file contents to `"credentials"`.<br/>
    Remove the other entry. I.e. if you have updated `"credentials_file"`, remove `"credentials"` and vice versa.
4. Update [TinodeWeb](/tinode/webapp/) config [`firebase-init.js`](https://github.com/tinode/webapp/blob/master/firebase-init.js): update `messagingSenderId` and `messagingVapidKey`. These values are obtained from the https://console.firebase.google.com/.
5. Add `google-services.json` to [Tindroid](/tinode/tindroid/) by following instructions at https://developers.google.com/android/guides/google-services-plugin.

If you are using the official [Docker image](https://hub.docker.com/u/tinode):
1. Create a project at https://console.firebase.google.com if you have not done so already.
2. Follow instructions at https://cloud.google.com/iam/docs/creating-managing-service-account-keys to download the credentials file.
3. Follow instructions in the Docker [README](https://github.com/tinode/chat/tree/master/docker#enable-push-notifications) to enable push notifications.
4. Add `google-services.json` to [Tindroid](/tinode/tindroid/) by following instructions at https://developers.google.com/android/guides/google-services-plugin.
