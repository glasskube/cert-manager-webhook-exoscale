# cert-manager-webhook-exoscale

A cert-manager webhook solver implementation for [Exoscale DNS](https://www.exoscale.com/dns/).

## Installation

Make sure that cert-manager is installed before installing the webhook.
For more information, consult the [cert-manager documentation](https://cert-manager.io/docs/installation/).

Use helm to install the webhook:

<!-- x-release-please-start-version -->

```shell
helm install cert-manager-webhook-exoscale --namespace cert-manager \
  oci://ghcr.io/glasskube/charts/cert-manager-webhook-exoscale \
  --version 0.1.1 \
  --set groupName=acme.mycompany.com
```

<!-- x-release-please-end -->

The value used for `groupName` **must** be unique in your cluster.
For all available configuration values, check out the [`values.yaml`](./deploy/cert-manager-webhook-exoscale/values.yaml).

## Usage

With cert-manager and the webhook installed, you can reference the solver in an `Issuer` or `ClusterIssuer` to use it:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: exoscale-example
spec:
  acme:
    # Rest of the acme spec
    # ...
    solvers:
      - dns01:
          webhook:
            # Replace this with the groupName used during installation
            groupName: acme.mycompany.com
            solverName: exoscale
            config:
              apiKey:
                fromSecret:
                  name: exoscale-api
                  key: apiKey
              apiSecret:
                fromSecret:
                  name: exoscale-api
                  key: apiSecret
              # UUID of the Exoscale Domain (optional)
              # If omitted, the controller will select the correct zone
              # automatically
              domainId: ...
```

Check out the full example at [`examples/cluster-issuer`](./examples/cluster-issuer).

It is recommended to use secret references for the API key and secrets.
For `ClusterIssuer`s, the secret must be in the namespace where the webhook was installed.
By default, the webhook controller has permission to read all secrets in that namespace, although that can be restricted using helm values.
For `Issuer`s, the secret must be in the same namespace as the `Issuer`.
By default, the webhook controller usually **does not** have permission to read that secret, so you have to allow it explicitly.

## Required IAM Permissions

The following IAM policy is recommended for the webhook controller:

```json
{
  "default-service-strategy": "deny",
  "services": {
    "dns": {
      "type": "rules",
      "rules": [
        {
          "action": "allow",
          "expression": "operation in ['list-dns-domains', 'get-dns-domain', 'list-dns-domain-records', 'get-dns-domain-record', 'create-dns-domain-record', 'delete-dns-domain-record']"
        }
      ]
    }
  }
}
```

If the config contains a `domainId`, the `list-dns-domains` can be omitted.
