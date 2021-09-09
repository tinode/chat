echo "apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-dev
spec:
  acme:
    email: mgiglobal.tech@gmail.com
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef:
      name: letsencrypt-dev
    solvers:
    - http01:
        ingress:
          class: kong" | kubectl apply -f -

echo 'apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    kubernetes.io/tls-acme: "true"
    cert-manager.io/cluster-issuer: letsencrypt-dev
    kubernetes.io/ingress.class: kong
    nginx.ingress.kubernetes.io/secure-backends: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
    ingress.kubernetes.io/backend-protocol: "HTTPS"
  name: chat-mgi-vn-ingress
spec:
  rules:
  - host: chat-dev.mgi.vn
    http:
      paths:
      - pathType: Prefix
        path: /
        backend:
          service:
            name: chat-service
            port:
              number: 6060
  tls:
  - hosts:
    - chat-dev.mgi.vn
    secretName: chat-mgi-vn-dev' | kubectl apply -f -

# # check cert => kubectl describe certificate keycloak-auth
# kubectl patch ingress keycloak-ingress -p '{"metadata":{"annotations":{"konghq.com/protocols":"https"}}}'
# kubectl patch svc my-keycloak -p '{"metadata":{"annotations":{"konghq.com/protocol":"https"}}}'
