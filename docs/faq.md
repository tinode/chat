# Frequently Asked Questions


**Q**: I'm running Tinode server in a docker container. Where can I find server logs?<br/>
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
