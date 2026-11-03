import copy
import unittest

from binary_provenance import MAIN_MODULE, parse_build_info, verify_binary_build


REVISION = "a" * 40
VERSION = "go1.26.10"


class BinaryProvenanceTest(unittest.TestCase):
    def fixture(self):
        info = "\n".join([
            "mcnd: " + VERSION,
            "\tpath\t" + MAIN_MODULE + "/cmd/mcnd",
            "\tmod\t" + MAIN_MODULE + "\t(devel)\t",
            "\tdep\tgithub.com/cometbft/cometbft\tv0.38.26\th1:comet",
            "\tdep\tgithub.com/cosmos/cosmos-sdk\tv0.53.8\t",
            "\t=>\t./third_party/cosmos-sdk\t(devel)\t",
            "",
            "\tbuild\t-buildmode=exe", "\tbuild\t-compiler=gc", "\tbuild\t-trimpath=true",
            "\tbuild\tCGO_ENABLED=1", "\tbuild\tGOARCH=amd64", "\tbuild\tGOOS=linux",
            "\tbuild\tGOAMD64=v1", "\tbuild\tvcs=git", "\tbuild\tvcs.revision=" + REVISION,
            "\tbuild\tvcs.time=2026-11-03T08:14:08Z", "\tbuild\tvcs.modified=false",
        ]) + "\n"
        modules = [
            {"Main": True, "Path": MAIN_MODULE},
            {"Path": "github.com/cometbft/cometbft", "Version": "v0.38.26", "Sum": "h1:comet"},
            {"Path": "github.com/cosmos/cosmos-sdk", "Version": "v0.53.8", "Replace": {"Path": "./third_party/cosmos-sdk"}},
        ]
        return info, [{"Module": module} for module in modules]

    def verify(self, info, packages):
        return verify_binary_build(info, ".", REVISION, VERSION, environment={}, package_loader=lambda: packages)

    def test_default_metadata_and_local_replacement_match_actual_package_graph(self):
        info, packages = self.fixture()
        packages.append(copy.deepcopy(packages[-1]))  # Multiple packages in one module.
        actual = self.verify(info, packages)
        self.assertEqual("v0.53.8", actual["dependencies"]["github.com/cosmos/cosmos-sdk"]["version"])
        self.assertEqual("./third_party/cosmos-sdk", actual["dependencies"]["github.com/cosmos/cosmos-sdk"]["replace"]["path"])

    def test_recorded_toolchain_target_instrumentation_and_vcs_mismatches_reject(self):
        info, packages = self.fixture()
        for before, after in [
            (VERSION, "go1.26.9"), ("/cmd/mcnd", "/cmd/other"), ("-buildmode=exe", "-buildmode=pie"),
            ("-trimpath=true", "-trimpath=false"), ("CGO_ENABLED=1", "CGO_ENABLED=0"),
            ("GOAMD64=v1", "GOAMD64=v3"), ("vcs.modified=false", "vcs.modified=true"),
            ("vcs.revision=" + REVISION, "vcs.revision=" + "b" * 40),
        ]:
            with self.subTest(after=after), self.assertRaises(ValueError):
                self.verify(info.replace(before, after), packages)
        # Ordinary metadata fixtures only; no unsupported executable is built.
        for setting in ("-tags=custom", "-race=true", "-msan=true", "-asan=true", "GOEXPERIMENT=custom"):
            with self.subTest(setting=setting), self.assertRaisesRegex(ValueError, "unsupported default-build settings"):
                self.verify(info + "\tbuild\t" + setting + "\n", packages)

    def test_version_checksum_and_replacement_identity_are_all_required(self):
        info, packages = self.fixture()
        for before, after in [("v0.38.26", "v0.38.25"), ("h1:comet", "h1:different"),
                              ("./third_party/cosmos-sdk", "./another-sdk"), ("v0.53.8", "v0.53.7")]:
            with self.subTest(after=after), self.assertRaisesRegex(ValueError, "default package graph"):
                self.verify(info.replace(before, after), packages)
        with self.assertRaisesRegex(ValueError, "default package graph"):
            self.verify(info, packages[:-1])
        with self.assertRaisesRegex(ValueError, "default package graph"):
            self.verify(info, packages[1:])

    def test_duplicate_truncated_or_inconsistent_records_fail(self):
        info, packages = self.fixture()
        for suffix in ("\tbuild\tGOOS=linux\n", "\tdep\tgithub.com/cometbft/cometbft\tv0.38.26\th1:comet\n", "\t=>\tmissing-parent\t(devel)\n", "\tdep\ttruncated\n"):
            with self.subTest(suffix=suffix), self.assertRaises(ValueError):
                parse_build_info(info + suffix)
        wrong = copy.deepcopy(packages[-1])
        wrong["Module"]["Version"] = "v0.53.7"
        with self.assertRaisesRegex(ValueError, "inconsistent"):
            self.verify(info, packages + [wrong])
        with self.assertRaisesRegex(ValueError, "incomplete"):
            self.verify(info, packages + [{"Incomplete": True}])


if __name__ == "__main__":
    unittest.main()
