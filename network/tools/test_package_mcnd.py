import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from binary_provenance import MAIN_MODULE, parse_build_info
from package_mcnd import package
from verify_software_package import verify

REVISION="a"*40

class SoftwarePackageTest(unittest.TestCase):
    def fixture(self,directory,version="1.0.0"):
        source=Path(directory)/"source";(source/"network").mkdir(parents=True)
        (source/"LICENSE").write_text("preserved upstream license\n")
        (source/"network/go.mod").write_text("module "+MAIN_MODULE+"\n\ngo 1.26.11\n")
        binary=Path(directory)/"mcnd";binary.write_bytes(b"\x7fELF\x02\x01"+bytes(12)+b"\x3e\x00"+b"fixture")
        settings={"-buildmode":"exe","-compiler":"gc","-trimpath":"true","CGO_ENABLED":"1","GOOS":"linux","GOARCH":"amd64","GOAMD64":"v1","vcs":"git","vcs.revision":REVISION,"vcs.modified":"false"}
        info="mcnd: go1.26.11\n\tpath\t"+MAIN_MODULE+"/cmd/mcnd\n\tmod\t"+MAIN_MODULE+"\t(devel)\t\n"+"".join("\tbuild\t"+k+"="+v+"\n" for k,v in settings.items())
        metadata=parse_build_info(info)
        inventory={"sourceRevision":REVISION,"files":{"network/go.mod":hashlib.sha256((source/"network/go.mod").read_bytes()).hexdigest()}}
        def output(args,**kwargs):
            if args[0]=="git":return REVISION if "rev-parse" in args else ""
            if args[:3]==["go","version","-m"]:return info
            if args[0]==str(binary):return json.dumps({"server_name":"mcnd","commit":REVISION,"version":version})
            raise AssertionError(args)
        return source,binary,metadata,inventory,output

    def build(self,directory,*,version="1.0.0",runtime_version=None):
        source,binary,metadata,inventory,output=self.fixture(directory,runtime_version or version or "0.0.0-dev")
        archive=Path(directory)/"mcnd.tar.gz"
        with patch("package_mcnd.subprocess.check_output",side_effect=output),patch("package_mcnd.verify_binary_build",return_value=metadata),patch("package_mcnd.verify_build_inputs",return_value=inventory),patch("package_mcnd.platform.system",return_value="Linux"),patch("package_mcnd.platform.machine",return_value="x86_64"):
            manifest=package(binary,source,REVISION,archive,release_version=version)
        return archive,manifest

    def test_explicit_release_preserves_inventory_sbom_licenses_without_genesis(self):
        with tempfile.TemporaryDirectory() as directory:
            archive,manifest=self.build(directory)
            self.assertEqual("software-release",manifest["status"])
            self.assertFalse(manifest["includesGenesis"])
            report=verify(archive,REVISION,"1.0.0")
            self.assertEqual("1.0.0",report["releaseVersion"])
            with tarfile.open(archive) as tar:
                self.assertIn("provenance/LICENSE",tar.getnames());self.assertNotIn("genesis.json",tar.getnames());self.assertNotIn("DEVELOPMENT.txt",tar.getnames())
                self.assertEqual("1.0.0",json.load(tar.extractfile("SBOM.cdx.json"))["metadata"]["component"]["version"])

    def test_default_remains_development_and_release_requires_actual_runtime_version(self):
        with tempfile.TemporaryDirectory() as directory:
            archive,manifest=self.build(directory,version=None)
            self.assertEqual("development",manifest["status"])
            with tarfile.open(archive) as tar:self.assertIn("DEVELOPMENT.txt",tar.getnames())
            with self.assertRaisesRegex(ValueError,"missing required|unexpected"):
                verify(archive,REVISION,"1.0.0")
        with tempfile.TemporaryDirectory() as directory,self.assertRaisesRegex(ValueError,"runtime binary release version mismatch"):
            self.build(directory,runtime_version="0.0.0-dev")

    def test_verifier_rejects_source_version_checksum_extra_keys_and_links(self):
        with tempfile.TemporaryDirectory() as directory:
            archive,_=self.build(directory)
            with self.assertRaisesRegex(ValueError,"manifest identity"):
                verify(archive,"b"*40,"1.0.0")
            with self.assertRaisesRegex(ValueError,"manifest identity"):
                verify(archive,REVISION,"1.0.1")
            with tarfile.open(archive) as tar:files={m.name:tar.extractfile(m).read() for m in tar}
            variants=[]
            changed=dict(files);changed["bin/mcnd"]+=b"tampered";variants.append(changed)
            changed=dict(files);changed["genesis.json"]=b"{}";variants.append(changed)
            changed=dict(files);changed["provenance/priv_validator_key.json"]=b"{}";variants.append(changed)
            for i,rows in enumerate(variants):
                target=Path(directory)/f"bad-{i}.tar.gz"
                with tarfile.open(target,"w:gz",format=tarfile.USTAR_FORMAT) as tar:
                    for name,data in rows.items():m=tarfile.TarInfo(name);m.size=len(data);tar.addfile(m,io.BytesIO(data))
                with self.assertRaisesRegex(ValueError,"SHA256|unexpected"):
                    verify(target,REVISION,"1.0.0")
            target=Path(directory)/"link.tar.gz"
            with tarfile.open(target,"w:gz",format=tarfile.USTAR_FORMAT) as tar:
                m=tarfile.TarInfo("bin/mcnd");m.type=tarfile.SYMTYPE;m.linkname="/etc/passwd";tar.addfile(m)
            with self.assertRaisesRegex(ValueError,"unexpected"):
                verify(target,REVISION,"1.0.0")

if __name__=="__main__":unittest.main()
