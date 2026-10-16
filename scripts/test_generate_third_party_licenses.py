#!/usr/bin/env python3
"""Tests for scripts/generate-third-party-licenses.py - the third-party manifest generator.

Run: python3 -m unittest scripts.test_generate_third_party_licenses  (or python3 scripts/test_generate_third_party_licenses.py)

The committed THIRD_PARTY_LICENSES.md / LICENSES/ must be byte-stable or the
ci-licenses regenerate-and-diff check is flaky. These tests pin the pieces that
make it deterministic: license-text classification, name sanitization, the
parsers for each ecosystem's input format, and the markdown renderer.
"""

import importlib.machinery
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

_MODULE_PATH = Path(__file__).with_name("generate-third-party-licenses.py")
_spec = importlib.util.spec_from_loader(
    "generate_third_party_licenses",
    loader=importlib.machinery.SourceFileLoader("generate_third_party_licenses", str(_MODULE_PATH)),
)
gpl = importlib.util.module_from_spec(_spec)
sys.modules["generate_third_party_licenses"] = gpl
_spec.loader.exec_module(gpl)


class ClassifyLicenseText(unittest.TestCase):
    def test_apache_2_0(self):
        text = (
            "                                 Apache License\n"
            "                           Version 2.0, January 2004\n"
            "                        http://www.apache.org/licenses/\n"
        )
        self.assertEqual(gpl.classify_license_text(text), "Apache-2.0")

    def test_mit(self):
        text = (
            "MIT License\n\n"
            "Permission is hereby granted, free of charge, to any person obtaining a copy\n"
            "of this software and associated documentation files (the \"Software\")...\n"
        )
        self.assertEqual(gpl.classify_license_text(text), "MIT")

    def test_bsd3_requires_neither_the_name(self):
        text = (
            "Redistributions of source code must retain the above copyright notice,\n"
            "this list of conditions and the following disclaimer.\n"
            "Neither the name of the copyright holder nor the names of its\n"
            "contributors may be used to endorse or promote products\n"
        )
        self.assertEqual(gpl.classify_license_text(text), "BSD-3-Clause")

    def test_bsd2_without_advertising_clause(self):
        text = (
            "Redistributions of source code must retain the above copyright\n"
            "notice, this list of conditions and the following disclaimer.\n"
            "Redistributions in binary form must reproduce the above\n"
        )
        self.assertEqual(gpl.classify_license_text(text), "BSD-2-Clause")

    def test_isc(self):
        text = (
            "Permission to use, copy, modify, and/or distribute this software for any\n"
            "purpose with or without fee is hereby granted.\n"
        )
        self.assertEqual(gpl.classify_license_text(text), "ISC")

    def test_mpl_2_0(self):
        self.assertEqual(
            gpl.classify_license_text("Mozilla Public License Version 2.0\n=="), "MPL-2.0"
        )

    def test_epl_2_0(self):
        self.assertEqual(
            gpl.classify_license_text("Eclipse Public License - v 2.0\n"), "EPL-2.0"
        )

    def test_unlicense(self):
        self.assertEqual(
            gpl.classify_license_text(
                "This is free and unencumbered software released into the public domain."
            ),
            "Unlicense",
        )

    def test_unknown(self):
        self.assertEqual(gpl.classify_license_text("do whatever you want"), "Unknown")

    def test_whitespace_and_case_normalization(self):
        self.assertEqual(
            gpl.classify_license_text("  apache   LICENSE   version\n 2.0  "), "Apache-2.0"
        )


class Names(unittest.TestCase):
    def test_sanitize_dir_name(self):
        self.assertEqual(gpl.sanitize_dir_name("@sentry/browser"), "@sentry+browser")
        self.assertEqual(
            gpl.sanitize_dir_name("github.com/prometheus/client_golang"),
            "github.com+prometheus+client_golang",
        )

    def test_dep_dir(self):
        self.assertEqual(gpl.dep_dir("dompurify", "3.4.16"), "dompurify@3.4.16")

    def test_license_file_regex(self):
        rx = gpl.LICENSE_FILE_RE
        for name in ("LICENSE", "license", "LICENCE.txt", "LICENSE-MPL",
                     "LICENSE.libyaml", "UNLICENSE", "COPYING", "NOTICE"):
            self.assertTrue(rx.match(name), name)
        for name in ("README.md", "readme", "package.json", "LICENSING-PLAN.txt"):
            self.assertFalse(rx.match(name), name)


class PnpmParsing(unittest.TestCase):
    def test_concatenated_json_documents(self):
        raw = '{"MIT": [{"name": "a", "versions": ["1.0.0"]}]}{"ISC": []}'
        docs = gpl.parse_pnpm_licenses_json(raw)
        self.assertEqual(len(docs), 2)
        self.assertEqual(docs[0]["MIT"][0]["name"], "a")

    def test_declared_license_forms(self):
        self.assertEqual(gpl.declared_license_to_spdx("MIT"), "MIT")
        self.assertEqual(gpl.declared_license_to_spdx({"type": "MIT", "url": "x"}), "MIT")
        self.assertEqual(gpl.declared_license_to_spdx(None), None)
        self.assertEqual(gpl.declared_license_to_spdx(""), None)
        self.assertEqual(
            gpl.declared_license_to_spdx([{"type": "MIT"}, "Apache-2.0"]),
            "MIT OR Apache-2.0",
        )


class NpmLockfileParsing(unittest.TestCase):
    def test_skips_root_and_extracts_entries(self):
        lock = {
            "packages": {
                "": {"name": "ext", "version": "0.1.0"},
                "node_modules/prom-client": {"version": "15.1.3", "license": "Apache-2.0"},
                "node_modules/@scope/pkg": {"version": "1.2.3"},
                "node_modules/no-version": {"license": "MIT"},
                "node_modules/typescript": {"version": "6.0.3", "license": "Apache-2.0", "dev": True},
            }
        }
        rows = gpl.parse_npm_lockfile(lock)
        self.assertEqual(
            [(r["name"], r["version"]) for r in rows],
            [("prom-client", "15.1.3"), ("@scope/pkg", "1.2.3")],
        )
        self.assertEqual(rows[0]["license"], "Apache-2.0")
        self.assertIsNone(rows[1]["license"])


class GoParsing(unittest.TestCase):
    def test_parse_go_work_use_block(self):
        text = (
            "go 1.26.1\n\nuse (\n\t./operators/agent\n\t./services/hub\n"
            "\t// a comment\n\t./shared/api\n)\n"
        )
        self.assertEqual(
            gpl.parse_go_work_use(text),
            ["operators/agent", "services/hub", "shared/api"],
        )

    def test_parse_go_list_modules_filters_std_and_own(self):
        out = (
            "\n"
            "github.com/a/b v1.0.0\n"
            "std \n"
            "github.com/DominikPinsel/ainsel/shared/api \n"
            "github.com/a/b v1.0.0\n"
            "github.com/c/d v0.1.0\n"
        )
        self.assertEqual(
            gpl.parse_go_list_modules(out),
            [("github.com/a/b", "v1.0.0"), ("github.com/c/d", "v0.1.0")],
        )

    def test_go_module_cache_dir_escapes_uppercase(self):
        d = gpl.go_module_cache_dir(Path("/cache"), "github.com/MicahParks/keyfunc/v3", "v3.8.0")
        self.assertEqual(
            str(d), "/cache/github.com/!micah!parks/keyfunc/v3@v3.8.0"
        )


class CanonicalFallbacks(unittest.TestCase):
    def test_dep_without_text_gets_shared_canonical(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "LICENSE").write_text("Apache License, Version 2.0")
            mit = gpl.Dep("pi", "a", "1.0.0", "MIT", "a@1.0.0", ())
            apache = gpl.Dep("pi", "b", "1.0.0", "Apache-2.0", "b@1.0.0", ())
            unknown = gpl.Dep("pi", "c", "1.0.0", "Unknown", "c@1.0.0", ())
            out = gpl.apply_canonical_fallbacks([mit, apache, unknown], root)
            self.assertEqual([t.filename for t in out[0].texts], ["MIT.txt"])
            self.assertTrue(out[0].texts[0].shared)
            self.assertEqual([t.filename for t in out[1].texts], ["Apache-2.0.txt"])
            self.assertEqual(out[1].texts[0].data, b"Apache License, Version 2.0")
            self.assertEqual(out[2].texts, ())
            md = gpl.render_markdown(out)
            self.assertIn("[MIT.txt](LICENSES/MIT.txt)", md)
            gpl.write_outputs(root, out)
            self.assertTrue((root / "LICENSES" / "MIT.txt").is_file())
            self.assertTrue((root / "LICENSES" / "Apache-2.0.txt").is_file())


class Dedupe(unittest.TestCase):
    def test_dedupe_within_ecosystem_keeps_first_per_name_version(self):
        src = Path("/dev/null")
        a1 = gpl.Dep("pi", "ws", "8.22.0", "MIT", "ws@8.22.0", ())
        a2 = gpl.Dep("pi", "ws", "8.22.0", "MIT", "ws@8.22.0", ())
        b = gpl.Dep("node", "ws", "8.22.0", "MIT", "ws@8.22.0", ())
        c = gpl.Dep("pi", "ws", "8.21.0", "MIT", "ws@8.21.0", ())
        out = gpl.dedupe_within_ecosystem([a2, c, b, a1])
        self.assertEqual(
            [(d.ecosystem, d.version) for d in out],
            [("node", "8.22.0"), ("pi", "8.21.0"), ("pi", "8.22.0")],
        )


class Rendering(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        src = Path(self._tmp.name) / "LICENSE"
        self.src_data = b"MIT License\nPermission is hereby granted, free of charge\n"
        (Path(self._tmp.name) / "LICENSE").write_bytes(self.src_data)

    def tearDown(self):
        self._tmp.cleanup()

    def _deps(self):
        a = gpl.Dep("node", "b-pkg", "1.0.0", "MIT", "b-pkg@1.0.0",
                    (gpl.LicenseText("LICENSE", self.src_data, "MIT"),))
        b = gpl.Dep("node", "A-pkg", "2.0.0",
                    "(MPL-2.0 OR Apache-2.0) — Apache-2.0 elected (see NOTICE)",
                    "A-pkg@2.0.0", ())
        c = gpl.Dep("go", "github.com/x/y", "v1.2.3", "Apache-2.0",
                    "github.com+x+y@v1.2.3", (gpl.LicenseText("LICENSE", self.src_data, "Apache-2.0"),))
        return [a, b, c]

    def test_sorted_case_insensitively_within_sections(self):
        md = gpl.render_markdown(self._deps())
        idx_a = md.index("| `A-pkg`")
        idx_b = md.index("| `b-pkg`")
        self.assertLess(idx_a, idx_b)
        self.assertIn("## Go modules", md)

    def test_election_annotation_and_links(self):
        md = gpl.render_markdown(self._deps())
        self.assertIn("Apache-2.0 elected (see NOTICE)", md)
        self.assertIn("[LICENSE](LICENSES/b-pkg@1.0.0/LICENSE)", md)
        self.assertIn("[LICENSE](LICENSES/github.com+x+y@v1.2.3/LICENSE)", md)

    def test_deterministic_output(self):
        deps = self._deps()
        self.assertEqual(gpl.render_markdown(deps), gpl.render_markdown(list(reversed(deps))))

    def test_write_outputs_copies_and_prunes(self):
        deps = self._deps()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "LICENSES" / "stale@0.0.1").mkdir(parents=True)
            (root / "LICENSES" / "stale@0.0.1" / "LICENSE").write_text("old")
            (root / "LICENSES" / "stale-file.txt").write_text("old")
            gpl.write_outputs(root, deps)
            self.assertTrue((root / "LICENSES" / "b-pkg@1.0.0" / "LICENSE").is_file())
            self.assertEqual(
                (root / "LICENSES" / "b-pkg@1.0.0" / "LICENSE").read_text(),
                "MIT License\nPermission is hereby granted, free of charge\n",
            )
            self.assertFalse((root / "LICENSES" / "stale@0.0.1").exists())
            self.assertFalse((root / "LICENSES" / "stale-file.txt").exists())
            self.assertTrue((root / "THIRD_PARTY_LICENSES.md").is_file())

    def test_write_outputs_is_byte_stable(self):
        deps = self._deps()
        with tempfile.TemporaryDirectory() as tmp1, tempfile.TemporaryDirectory() as tmp2:
            gpl.write_outputs(Path(tmp1), deps)
            gpl.write_outputs(Path(tmp2), list(reversed(deps)))
            self.assertEqual(
                (Path(tmp1) / "THIRD_PARTY_LICENSES.md").read_bytes(),
                (Path(tmp2) / "THIRD_PARTY_LICENSES.md").read_bytes(),
            )
            self.assertEqual(
                (Path(tmp1) / "LICENSES" / "b-pkg@1.0.0" / "LICENSE").read_bytes(),
                (Path(tmp2) / "LICENSES" / "b-pkg@1.0.0" / "LICENSE").read_bytes(),
            )


if __name__ == "__main__":
    unittest.main()
