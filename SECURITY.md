# Security Policy

## Reporting a Vulnerability

Please report a vulnerability to `security@tinode.co`.

## What not to report

 * Firebase initialization tokens. The Firebase tokens are really public: they must be included into client applications and consequently are not private by design.
 * Exposed `/pprof` or `/expvar`. We know they are exposed. It's intentional and harmless.
 * Exposed Prometheus metrics `/metrics`. Like above, it's intentional and harmless.

