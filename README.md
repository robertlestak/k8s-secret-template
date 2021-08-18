# k8s secret template

Enables kubernetes operators to define a set of secret _template_ files that can be safely re-applied to a cluster without the risk of losing any data.

## Problem Statement

Certain kubernetes secrets (such as those managed by `cert-manager`) are applied to the cluster empty, and the secret data is managed and updated by a cluster-internal operator.

If automation `kubectl apply -f secret.yaml` is used to apply the empty secret after the data has been dynamically added by the operator, the data will be lost.

This conflicts with IaaC (Infrastructure as Code) best practices, as the definition of the secret object is not repeatable if there is a manual process and de-link from automation to manage the state of the secret resource.

## Solution

This script iterates through all `yaml` secrets to be applied to the cluster, and then queries the cluster for the current state of the secret.

It then updates the value of the `data` field in the new secret to match that of the existing secret, before applying the new secret to the cluster.

In this way, this provides a "safe" way to continually apply the empty secret to the cluster without losing any data. 

## Caveats

This script is intended to blindly override the value of the local secret with the value that currently exists in the cluster and will create an empty secret if one does not already exist.

It goes without saying, this should only be used for a very specific use case, most of the time `kubectl create` / `kubectl apply` will suit your needs just fine.