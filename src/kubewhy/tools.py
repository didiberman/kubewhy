"""Read-only Kubernetes tools.

Every function in this module issues exactly one kind of API call: get, list,
watch, or a subresource read (logs). None of them can mutate cluster state.
That's not just a convention here -- kubewhy is meant to be run against a
ServiceAccount whose ClusterRole only grants get/list/watch (see
deploy/readonly-clusterrole.yaml), so even a prompt-injected or hallucinating
model gets a 403 from the API server if it tries anything else.
"""
from __future__ import annotations

from kubernetes import client, config
from kubernetes.client.rest import ApiException

_core: client.CoreV1Api | None = None
_apps: client.AppsV1Api | None = None
_custom: client.CustomObjectsApi | None = None


def load_kube_client() -> None:
    global _core, _apps, _custom
    try:
        config.load_kube_config()
    except config.ConfigException:
        config.load_incluster_config()
    _core = client.CoreV1Api()
    _apps = client.AppsV1Api()
    _custom = client.CustomObjectsApi()


def _fmt_api_error(e: ApiException) -> str:
    return f"API error {e.status}: {e.reason}"


def get_resource(kind: str, namespace: str, name: str | None = None, label_selector: str | None = None) -> dict:
    """Get or list a resource: pod, deployment, replicaset, service, node, event."""
    kind = kind.lower()
    try:
        if kind == "pod":
            if name:
                return _core.read_namespaced_pod(name, namespace).to_dict()
            return [p.to_dict() for p in _core.list_namespaced_pod(namespace, label_selector=label_selector).items]
        if kind == "deployment":
            if name:
                return _apps.read_namespaced_deployment(name, namespace).to_dict()
            return [d.to_dict() for d in _apps.list_namespaced_deployment(namespace, label_selector=label_selector).items]
        if kind == "replicaset":
            return [r.to_dict() for r in _apps.list_namespaced_replica_set(namespace, label_selector=label_selector).items]
        if kind == "service":
            if name:
                return _core.read_namespaced_service(name, namespace).to_dict()
            return [s.to_dict() for s in _core.list_namespaced_service(namespace).items]
        if kind == "node":
            if name:
                return _core.read_node(name).to_dict()
            return [n.to_dict() for n in _core.list_node().items]
        raise ValueError(f"unsupported kind for get_resource: {kind}")
    except ApiException as e:
        return {"error": _fmt_api_error(e)}


def describe_pod(namespace: str, name: str) -> dict:
    """Pod spec/status plus the events involving it -- the read-only equivalent of `kubectl describe pod`."""
    try:
        pod = _core.read_namespaced_pod(name, namespace).to_dict()
    except ApiException as e:
        return {"error": _fmt_api_error(e)}
    events = get_events(namespace, involved_object=name)
    return {"pod": pod, "events": events}


def get_logs(namespace: str, pod: str, container: str | None = None, previous: bool = False, tail_lines: int = 200) -> str:
    try:
        return _core.read_namespaced_pod_log(
            name=pod,
            namespace=namespace,
            container=container,
            previous=previous,
            tail_lines=tail_lines,
        )
    except ApiException as e:
        return _fmt_api_error(e)


def get_events(namespace: str, involved_object: str | None = None) -> list[dict]:
    try:
        events = _core.list_namespaced_event(namespace).items
    except ApiException as e:
        return [{"error": _fmt_api_error(e)}]
    out = [e.to_dict() for e in events]
    if involved_object:
        out = [e for e in out if e.get("involved_object", {}).get("name") == involved_object]
    out.sort(key=lambda e: e.get("last_timestamp") or e.get("event_time") or "")
    return out


def top_pods(namespace: str) -> list[dict] | dict:
    """Requires metrics-server. Read-only -- hits the metrics.k8s.io aggregated API."""
    try:
        result = _custom.list_namespaced_custom_object(
            group="metrics.k8s.io", version="v1beta1", namespace=namespace, plural="pods"
        )
        return result.get("items", [])
    except ApiException as e:
        return {"error": f"metrics-server unavailable or not installed: {_fmt_api_error(e)}"}


def can_i(namespace: str, service_account: str, verb: str, resource: str) -> dict:
    """Read-only RBAC check via SelfSubjectAccessReview-style dry run using rbac.authorization.k8s.io lookups."""
    try:
        roles = _custom.list_namespaced_custom_object(
            group="rbac.authorization.k8s.io", version="v1", namespace=namespace, plural="rolebindings"
        )
        return {"rolebindings": roles.get("items", [])}
    except ApiException as e:
        return {"error": _fmt_api_error(e)}
