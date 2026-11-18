"""Small tests of recovery guardrails; actual consensus is tested in Linux CI."""

import base64
from datetime import datetime, timedelta, timezone
from contextlib import redirect_stderr
import io
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import native_launch_rehearsal as launch


class QuorumRecoveryTest(unittest.TestCase):
    def minority_nodes(self, *, proposal="B" * 64):
        validators = [{"address": str(i) * 40, "voting_power": "10000"} for i in range(1, 5)]
        round_state = {"height/round/step": "11/0/4", "proposal_block_hash": proposal,
                       "height_vote_set": [{"round": 0, "prevotes": ["nil-Vote"] * 4,
                                            "precommits": ["nil-Vote"] * 4}]}
        block_time = (datetime.now(timezone.utc) - timedelta(seconds=30)).isoformat()
        def node(index):
            identity = str(index) * 40
            responses = {"status": {"node_info": {"id": identity, "network": "mintcandy_990012-1"},
                                    "sync_info": {"latest_block_height": "10", "latest_block_hash": "A" * 64,
                                                  "latest_block_time": block_time, "catching_up": False}},
                         "consensus_state": {"round_state": round_state},
                         "validators?height=11": {"block_height": "11", "total": "4", "validators": validators}}
            return SimpleNamespace(node_id=identity, chain_id="mintcandy_990012-1", rpc=responses.__getitem__)
        return [node(1), node(2)]

    def test_actual_stale_snapshot_recommends_pause_without_authorizing_execution(self):
        result = launch.assess_stalled_minority(self.minority_nodes())
        self.assertTrue(result["valid_snapshot"])
        self.assertTrue(result["pause_new_bridge_processing"])
        self.assertIn("stale or future-dated source block", result["reasons"])
        self.assertFalse(result["bridge_execution_authorized"])
        self.assertFalse(result["cryptographically_verified"])

    def test_absent_proposal_is_a_valid_stalled_source_observation(self):
        result = launch.assess_stalled_minority(self.minority_nodes(proposal=""))
        self.assertTrue(result["valid_snapshot"])
        self.assertTrue(result["pause_new_bridge_processing"])
        self.assertIn("stale or future-dated source block", result["reasons"])

    def test_invalid_snapshot_cannot_pass_as_stall_diagnosis(self):
        invalid = {"valid_snapshot": False, "reasons": ["invalid snapshot: malformed validators"],
                   "pause_new_bridge_processing": True, "bridge_execution_authorized": False,
                   "cryptographically_verified": False}
        report = {}
        with patch.object(launch, "assess", return_value=invalid):
            with self.assertRaisesRegex(ValueError, "invalid snapshot: malformed validators"):
                launch.assess_stalled_minority(self.minority_nodes(), report)
        self.assertEqual(report["haltMonitor"], invalid)
        self.assertEqual(report["haltMonitorSample"]["validatorCount"], 4)
        self.assertNotIn("raw", report["haltMonitorSample"])

    def test_cli_requires_four_validators_and_probe(self):
        required = ["--binary", "node", "--binary-sha256", "a" * 64, "--archive-sha256", "b" * 64,
                    "--source-commit", "c" * 40, "--fixture", "fixture", "--home-parent", "temp", "--report", "report"]
        for flags in (["--quorum-loss-recovery"], ["--quorum-loss-recovery", "--four-validators"],
                      ["--commit-probe", "probe"], ["--quorum-loss-recovery", "--four-validators",
                       "--commit-probe", "probe", "--treasury-transfer"]):
            with self.subTest(flags=flags), redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as error:
                    launch.main(required + flags)
                self.assertEqual(error.exception.code, 2)

    def test_restart_rejects_running_signer(self):
        node = SimpleNamespace(child=SimpleNamespace(poll=lambda: None), home=Path("node"))
        with self.assertRaisesRegex(ValueError, "second signer"):
            launch.restart_original_signer(node, "binary", {}, object(), b"signing-state")

    def test_restart_rejects_changed_signing_state(self):
        node = SimpleNamespace(child=SimpleNamespace(poll=lambda: 0), home=Path("node"))
        with patch.object(launch, "private_bytes", return_value=b"changed"):
            with self.assertRaisesRegex(ValueError, "signing state changed"):
                launch.restart_original_signer(node, "binary", {}, object(), b"original")

    def test_probe_payload_uses_actual_chain_power_and_missing_member(self):
        rows = [{"name": name, "consensusAddress": str(i) * 40,
                 "publicKey": base64.b64encode(bytes([i]) * 32).decode()}
                for i, name in enumerate("abcd", 1)]
        header = {"block_hash": "B" * 64, "app_hash": "A" * 64}
        signed = {"header": {"chain_id": "mintcandy_990012-1", "height": "21"},
                  "commit": {"height": "21", "block_id": {"hash": "B" * 64}}}
        nodes = [SimpleNamespace(chain_id="mintcandy_990012-1",
                                 rpc=lambda _: {"canonical": True, "signed_header": signed})]
        verified = [row["consensusAddress"] for row in rows[:3]]
        process = SimpleNamespace(returncode=0, stderr="", stdout=json.dumps(verified))
        with patch.object(launch, "committed_header", return_value=header), \
                patch.object(launch.subprocess, "run", return_value=process) as run:
            result = launch.verify_quorum_commit(nodes, rows, (10000,) * 4, 21, "probe", missing=("d",))
        payload = json.loads(run.call_args.kwargs["input"])
        self.assertEqual(payload["chainId"], "mintcandy_990012-1")
        self.assertEqual(payload["votingPowers"], [10000] * 4)
        self.assertEqual(payload["allowedMissing"], [rows[3]["consensusAddress"]])
        self.assertEqual(result["verifiedEd25519Signatures"], 3)
        self.assertEqual(result["signedPower"], 30000)

    def test_corrupt_probe_output_does_not_accept_repeated_signer(self):
        rows = [{"name": name, "consensusAddress": str(i) * 40,
                 "publicKey": base64.b64encode(bytes([i]) * 32).decode()}
                for i, name in enumerate("abcd", 1)]
        signed = {"header": {"chain_id": "mintcandy_990012-1", "height": "21"},
                  "commit": {"height": "21", "block_id": {"hash": "B" * 64}}}
        nodes = [SimpleNamespace(chain_id="mintcandy_990012-1",
                                 rpc=lambda _: {"canonical": True, "signed_header": signed})]
        repeated = [rows[0]["consensusAddress"]] * 3
        with patch.object(launch, "committed_header", return_value={"block_hash": "B" * 64}), \
                patch.object(launch.subprocess, "run", return_value=SimpleNamespace(
                    returncode=0, stderr="", stdout=json.dumps(repeated))):
            with self.assertRaisesRegex(ValueError, "cryptographic recovery signers"):
                launch.verify_quorum_commit(nodes, rows, (1,) * 4, 21, "probe", missing=("d",))


if __name__ == "__main__":
    unittest.main()
