#!/usr/bin/env python3
"""Golden-file tests for the agent egress rendering (issue #356).

Renders the chart with the egress proxy off (the default) and on, extracts
the NetworkPolicy objects plus the proxy's own resources, and compares them
against checked-in golden files. Also asserts the security-relevant
invariants directly, so a template change that silently reopens the exfil
path fails even before the golden diff is reviewed:

* proxy off: agent-egress keeps the open 443/TCP rule (existing installs
  unchanged apart from the intentional DNS scoping) and no proxy resources
  render at all.
* the DNS pod selector is a value: the scoped port-53 rule matches the DNS pods
  by label as well as by namespace, and `networkPolicy.dnsPodSelector` replaces
  the default `k8s-app: kube-dns` — the upstream label is not universal
  (OpenShift labels its DNS pods `dns=default`), and a cluster whose DNS pods
  carry neither would otherwise lose name resolution with nothing to set.
* proxy on: agent-egress has NO 443 rule anywhere, only hub 8080, scoped DNS
  and the proxy on the configured port; the egress-proxy policy is the only
  443-capable rule and it opens its own Ingress for the agent pods (the
  namespace-wide default-deny-ingress is enforced on the proxy too); the squid
  ConfigMap carries the merged preset + free-form allow-list as one complete
  `acl` line per domain (Squid parses a directive per line, and nothing in CI
  runs `squid -k parse`), with the localhost manager allowances the exec
  probes need ordered ahead of every deny; the agent operator Deployment gets
  EGRESS_PROXY_URL / EGRESS_NO_PROXY.

Run with --update after intentional template changes to regenerate the
goldens, then review the diff.
"""

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHART = ROOT / "chart"
GOLDEN_DIR = CHART / "tests" / "golden"
HELM = os.environ.get("HELM", "helm")

PROXY_OFF_SETS = []
PROXY_ON_SETS = [
    "agentEgress.proxy.enabled=true",
    "agentEgress.proxy.presets[0]=github",
    "agentEgress.proxy.presets[1]=npm",
    "agentEgress.proxy.allowedDomains[0]=api.openai.com",
]

SCENARIOS = {
    "egress-proxy-off.yaml": PROXY_OFF_SETS,
    "egress-proxy-on.yaml": PROXY_ON_SETS,
}


def render(value_sets):
    cmd = [HELM, "template", "ainsel", str(CHART)]
    for value_set in value_sets:
        cmd += ["--set", value_set]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit(f"helm template failed for {value_sets}:\n{result.stderr}")
    return result.stdout


def extract_egress(manifests):
    """Return the egress-relevant documents, comment-stripped, sorted."""
    docs = []
    for doc in re.split(r"(?m)^---$", manifests):
        is_policy = bool(re.search(r"(?m)^kind: NetworkPolicy$", doc))
        is_proxy_resource = bool(
            re.search(r"(?m)^  name: (agent-egress|agent-egress-proxy|egress-proxy)$", doc)
        )
        if not (is_policy or is_proxy_resource):
            continue
        lines = [
            line
            for line in doc.splitlines()
            # Drop source headers and version-dependent labels/annotations so
            # the goldens stay stable across chart version bumps.
            if not line.lstrip().startswith(("#", "helm.sh/chart:"))
            and "checksum/config:" not in line
        ]
        body = "\n".join(lines).strip()
        if body:
            docs.append(body)
    docs.sort()
    return "\n---\n".join(docs) + "\n"


def doc_named(manifests, kind, name):
    for doc in re.split(r"(?m)^---$", manifests):
        if re.search(rf"(?m)^kind: {kind}$", doc) and re.search(
            rf"(?m)^  name: {re.escape(name)}$", doc
        ):
            return doc
    return ""


def policy_named(manifests, name):
    return doc_named(manifests, "NetworkPolicy", name)


def check_invariants(name, manifests):
    agent_egress = policy_named(manifests, "agent-egress")
    egress_proxy = policy_named(manifests, "egress-proxy")
    assert agent_egress, "agent-egress policy must always render"
    # DNS exfil scoping applies in both modes (issue #356).
    assert "k8s-app: kube-dns" in agent_egress, (
        "agent-egress DNS rule must be scoped to the cluster DNS servers"
    )

    if name == "egress-proxy-off.yaml":
        assert "port: 443" in agent_egress, (
            "proxy-off render lost the open 443 rule (existing installs must "
            "keep the current egress posture)"
        )
        assert not egress_proxy, "egress-proxy policy must not render when off"
        for kind in ("Deployment", "Service", "ConfigMap", "ServiceAccount"):
            assert not doc_named(manifests, kind, "agent-egress-proxy"), (
                f"proxy-off render must not create the {kind} agent-egress-proxy"
            )
        operator = doc_named(manifests, "Deployment", "agent-operator")
        assert "EGRESS_PROXY_URL" not in operator, (
            "proxy-off render must not inject EGRESS_PROXY_URL (byte-stable "
            "operator Deployment for existing installs)"
        )
    else:
        assert "port: 443" not in agent_egress, (
            "agent-egress must have no 443 rule once the proxy is enabled"
        )
        assert "port: 3128" in agent_egress, (
            "agent-egress must allow the proxy port once the proxy is enabled"
        )
        assert egress_proxy, "egress-proxy policy missing with proxy enabled"
        assert "port: 443" in egress_proxy, (
            "egress-proxy policy must allow the proxy 443/TCP upstream"
        )
        # default-deny-ingress selects every pod in the namespace, and
        # NetworkPolicy is enforced on both ends: an egress rule on the agent
        # side does not open the proxy's ingress side.
        assert re.search(r"(?m)^\s+- Ingress$", egress_proxy), (
            "egress-proxy policy must declare the Ingress policy type: without "
            "its own allow rule the proxy pod receives nothing and enabling the "
            "feature blocks all agent HTTPS egress"
        )
        ingress_rules = egress_proxy.split("ingress:", 1)
        assert len(ingress_rules) == 2, "egress-proxy policy has no ingress section"
        ingress_rules = ingress_rules[1].split("egress:", 1)[0]
        assert "app.kubernetes.io/component: agents" in ingress_rules, (
            "egress-proxy ingress must be limited to the agent pods"
        )
        assert "port: 3128" in ingress_rules, (
            "egress-proxy ingress must allow the proxy port"
        )
        squid = doc_named(manifests, "ConfigMap", "agent-egress-proxy")
        assert squid, "squid ConfigMap missing with proxy enabled"
        for host in (
            "api.github.com",
            "github.com",
            "registry.npmjs.org",
            "api.openai.com",
        ):
            assert re.search(
                rf"(?m)^\s*acl allowed_hosts dstdomain {re.escape(host)}$", squid
            ), f"squid allow-list missing {host}"
        assert re.search(r"(?m)^\s*acl allowed_hosts dstdomain\s*$", squid) is None, (
            "squid allow-list rendered an `acl` line with no argument: Squid "
            "terminates a directive at the newline, so a multi-line value list "
            "without continuations is a config parse error and squid exits at "
            "startup — render one complete acl line per domain"
        )
        assert "http_access allow manager localhost" in squid, (
            "squid config must allow manager access from localhost, or the "
            "squidclient mgr:info readiness/liveness probes are refused and the "
            "proxy pod never becomes Ready"
        )
        manager_allow = squid.find("http_access allow manager localhost")
        deny_unsafe = squid.find("http_access deny !Safe_ports")
        assert 0 <= manager_allow < deny_unsafe, (
            "the localhost manager allow must precede deny !Safe_ports: the "
            "probes dial the proxy port, which is not a Safe_port here"
        )
        assert "pypi.org" not in squid, (
            "squid allow-list must only contain the enabled presets/domains"
        )
        deny = squid.find("http_access deny !allowed_hosts")
        allow = squid.find("http_access allow CONNECT allowed_hosts")
        assert 0 <= deny < allow, (
            "squid config must deny non-allow-listed hosts before any allow"
        )
        operator = doc_named(manifests, "Deployment", "agent-operator")
        assert "EGRESS_PROXY_URL" in operator, (
            "agent operator must receive EGRESS_PROXY_URL with proxy enabled"
        )
        assert "EGRESS_NO_PROXY" in operator, (
            "agent operator must receive EGRESS_NO_PROXY with proxy enabled"
        )


def check_dns_pod_selector():
    """The DNS pod label is a value, not a hardcoded constant.

    The scoped rule needs a pod selector — a namespaceSelector on kube-system
    alone would let an agent reach any pod there on 53 — but the label it
    defaults to is the upstream CoreDNS one, which OpenShift and some managed
    clusters do not use. So the selector must be settable, and setting it must
    replace the default rather than add to it.
    """
    default = render([])
    assert "k8s-app: kube-dns" in policy_named(default, "agent-egress"), (
        "the default render must keep the upstream CoreDNS selector"
    )

    dns_values = [
        "networkPolicy.dnsServerNamespace=openshift-dns",
        "networkPolicy.dnsPodSelector.dns=default",
        "networkPolicy.dnsPodSelector.k8s-app=null",
    ]
    agent_egress = policy_named(render(dns_values), "agent-egress")
    # The proxy's own DNS rule takes the selector too, so render the proxy on.
    proxy = policy_named(render(dns_values + PROXY_ON_SETS), "egress-proxy")
    assert "dns: default" in agent_egress and "k8s-app: kube-dns" not in agent_egress, (
        "setting networkPolicy.dnsPodSelector must replace the DNS pod label in "
        "agent-egress, not add to it"
    )
    assert "openshift-dns" in agent_egress, (
        "the DNS namespace must reach the agent-egress rule with the selector"
    )
    assert "dns: default" in proxy and "k8s-app: kube-dns" not in proxy, (
        "the egress-proxy DNS rule takes the same selector: with the proxy on, "
        "a proxy that cannot resolve is a proxy that connects to nothing"
    )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--update",
        action="store_true",
        help="regenerate golden files instead of comparing",
    )
    args = parser.parse_args()

    GOLDEN_DIR.mkdir(parents=True, exist_ok=True)
    check_dns_pod_selector()
    print("ok dns pod selector")
    failures = []
    for name, value_sets in SCENARIOS.items():
        rendered = render(value_sets)
        check_invariants(name, rendered)
        extracted = extract_egress(rendered)
        golden = GOLDEN_DIR / name
        if args.update:
            golden.write_text(extracted)
            print(f"updated {golden.relative_to(ROOT)}")
        elif not golden.exists():
            failures.append(f"{name}: golden file missing (run with --update)")
        elif golden.read_text() != extracted:
            failures.append(
                f"{name}: rendered egress resources differ from golden file "
                f"(if intentional, run scripts/test-chart-egress.py --update "
                f"and review the diff)"
            )
        else:
            print(f"ok {name}")

    if failures:
        for failure in failures:
            print(f"FAIL {failure}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
