import pathlib, tempfile, unittest
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


if __name__=='__main__':unittest.main()
