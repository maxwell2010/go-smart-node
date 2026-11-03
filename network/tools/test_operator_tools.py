import hashlib, os, pathlib, subprocess, tarfile, tempfile, unittest
from unittest.mock import patch
from configure_history import PROFILE, configure, render
from install_mcnd import validate_revision, install
from package_mcnd import package


class OperatorToolsTest(unittest.TestCase):
    def test_revision_rejects_branch_and_shell_text(self):
        for value in ('master', '123', '../file', 'a'*39, 'F'*40, 'a'*40+';command'):
            with self.assertRaises(ValueError): validate_revision(value)
        self.assertEqual('a'*40, validate_revision('a'*40))

    def fixture(self, root):
        home=pathlib.Path(root); (home/'config').mkdir();(home/'data').mkdir()
        for name,changes in PROFILE.items():
            text='';last=None
            for (section,key),value in changes.items():
                if section != last:text+='['+section+']\n';last=section
                text+=key+' = '+value+'\n'
            (home/'config'/name).write_text(text)
        return home

    def test_roles_keep_rpc_events_and_apply_backups(self):
        with tempfile.TemporaryDirectory() as root:
            home=self.fixture(root)
            original=(home/'config/config.toml').read_bytes()
            configure(home)
            self.assertEqual(original,(home/'config/config.toml').read_bytes())
            result=configure(home,True,'consensus')
            self.assertEqual('null',result[str(home/'config/config.toml')]['tx_index']['indexer'])
            self.assertEqual(original,(home/'config/config.toml.before-history').read_bytes())
            with self.assertRaises(ValueError):configure(home,True)

    def test_existing_database_is_never_changed(self):
        with tempfile.TemporaryDirectory() as root:
            home=self.fixture(root);(home/'data/application.db').mkdir()
            before={p:p.read_bytes() for p in (home/'config').iterdir()}
            with self.assertRaises(ValueError):configure(home,True)
            self.assertEqual(before,{p:p.read_bytes() for p in (home/'config').iterdir()})

    @unittest.skipIf(os.name=='nt','Linux symlink behavior tested in Linux CI')
    def test_dangling_backup_symlink_is_never_followed(self):
        with tempfile.TemporaryDirectory() as root:
            home=self.fixture(root);target=home/'outside'
            (home/'config/app.toml.before-history').symlink_to(target)
            original=(home/'config/app.toml').read_bytes()
            with self.assertRaises(ValueError):configure(home,True)
            self.assertFalse(target.exists())
            self.assertEqual(original,(home/'config/app.toml').read_bytes())

    def test_config_drift_rejected(self):
        with self.assertRaises(ValueError):render('pruning="default"\n',{(None,'missing'):'1'})
        with self.assertRaises(ValueError):render('pruning="default"\npruning="nothing"\n',{(None,'pruning'):'"custom"'})

    def test_installer_never_overwrites_existing_binary(self):
        with tempfile.TemporaryDirectory() as root:
            binary=pathlib.Path(root)/'mcnd';binary.write_bytes(b'existing executable')
            with self.assertRaises(ValueError):install('a'*40,binary)
            self.assertEqual(b'existing executable',binary.read_bytes())

    def test_packager_rejects_binary_from_different_revision(self):
        with tempfile.TemporaryDirectory() as root:
            source=pathlib.Path(root);binary=source/'mcnd'
            header=bytearray(20);header[:5]=b'\x7fELF\x02';header[18:20]=b'\x3e\x00';binary.write_bytes(header)
            def output(command,**kwargs):
                if 'rev-parse' in command:return 'a'*40+'\n'
                if 'status' in command:return ''
                return 'build -ldflags="-X main.buildCommit='+('b'*40)+'"'
            with patch('package_mcnd.subprocess.check_output',side_effect=output):
                with self.assertRaises(ValueError):package(binary,source,'a'*40,source/'rejected.tar.gz')
            self.assertFalse((source/'rejected.tar.gz').exists())

    def test_packager_rejects_missing_declared_sdk_source(self):
        with tempfile.TemporaryDirectory() as root:
            source=pathlib.Path(root);binary=source/'mcnd';binary.write_bytes(b'not used')
            (source/'network').mkdir()
            (source/'network/go.mod').write_text('replace github.com/cosmos/cosmos-sdk => ./third_party/cosmos-sdk\n')
            def output(command,**kwargs):
                if 'rev-parse' in command:return 'a'*40+'\n'
                return ''
            target=source/'rejected.tar.gz'
            with patch('package_mcnd.subprocess.check_output',side_effect=output), patch('package_mcnd.subprocess.run',side_effect=subprocess.CalledProcessError(1,['SDK verifier'])):
                with self.assertRaises(subprocess.CalledProcessError):package(binary,source,'a'*40,target)
            self.assertFalse(target.exists())

    def test_trimpath_requires_matching_native_cli_commit(self):
        for matches in (True, False):
            with self.subTest(matches=matches), tempfile.TemporaryDirectory() as root:
                source=pathlib.Path(root);binary=source/'mcnd'
                header=bytearray(20);header[:5]=b'\x7fELF\x02';header[18:20]=b'\x3e\x00';binary.write_bytes(header)
                def output(command, **kwargs):
                    if 'rev-parse' in command:return 'a'*40+'\n'
                    if 'status' in command:return ''
                    if command[0]=='go':return 'build -trimpath=true\n'
                    self.assertIn('--home',command)
                    self.assertTrue(pathlib.Path(command[command.index('--home')+1]).is_dir())
                    return '{"server_name":"mcnd","commit":"'+('a' if matches else 'b')*40+'"}'
                target=source/'package.tar.gz'
                with patch('package_mcnd.subprocess.check_output',side_effect=output), patch('package_mcnd.platform.system',return_value='Linux'), patch('package_mcnd.platform.machine',return_value='x86_64'):
                    if matches:
                        self.assertEqual('a'*40,package(binary,source,'a'*40,target)['sourceRevision'])
                    else:
                        with self.assertRaises(ValueError):package(binary,source,'a'*40,target)
                        self.assertFalse(target.exists())

    def test_packager_preserves_distinct_owned_licenses_and_notices(self):
        with tempfile.TemporaryDirectory() as root:
            source=pathlib.Path(root);binary=source/'mcnd'
            header=bytearray(20);header[:5]=b'\x7fELF\x02';header[18:20]=b'\x3e\x00';binary.write_bytes(header)
            notices={'COPYING':b'GPL notice\n','COPYING.LESSER':b'LGPL notice\n','crypto/library/NOTICE':b'Library notice\n'}
            for name,content in notices.items():
                path=source/'network/third_party/cosmos-geth'/name
                path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(content)
            def output(command,**kwargs):
                if 'rev-parse' in command:return 'a'*40+'\n'
                if 'status' in command:return ''
                return 'build -ldflags="-X main.buildCommit='+('a'*40)+'"'
            target=source/'development.tar.gz'
            with patch('package_mcnd.subprocess.check_output',side_effect=output):
                manifest=package(binary,source,'a'*40,target)
            with tarfile.open(target,'r:gz') as archive:
                for name,content in notices.items():
                    member='provenance/network/third_party/cosmos-geth/'+name
                    self.assertEqual(content,archive.extractfile(member).read())
                    self.assertEqual(hashlib.sha256(content).hexdigest(),manifest['files'][member])


if __name__=='__main__':unittest.main()
