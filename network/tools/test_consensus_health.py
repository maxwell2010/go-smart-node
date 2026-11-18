import contextlib
import copy
import datetime
import io
import json
from pathlib import Path
import tempfile
import sys
import unittest

from consensus_health import assess, load_snapshot, main, observe_round, required_power


NOW = datetime.datetime(2026, 11, 18, 5, 8, 10, tzinfo=datetime.timezone.utc)


def vote(index, validator, kind="prevotes", height=10, round_number=0, block="B" * 12):
    stage, title = ("PREVOTE", "Prevote") if kind == "prevotes" else ("PRECOMMIT", "Precommit")
    return (f"Vote{{{index}:{validator['address'][:12]} {height}/{round_number:02}/"
            f"SIGNED_MSG_TYPE_{stage}({title}) {block} {'C' * 12} @ 2026-11-18T05:08:09Z}}")


def fixture(powers=(1, 1, 1, 1)):
    validators = [{"address": f"{i + 1:012X}" + "A" * 28, "voting_power": str(power)}
                  for i, power in enumerate(powers)]
    state = {"height/round/step": "10/0/4", "proposal_block_hash": "B" * 64,
             "height_vote_set": [{"round": 0, "prevotes": [], "precommits": []}]}
    for kind in ("prevotes", "precommits"):
        state["height_vote_set"][0][kind] = [vote(i, v, kind) for i, v in enumerate(validators)]
    status = {"chain_id": "fixture_990012-1", "height": 9, "block_hash": "D" * 64,
              "block_time": "2026-11-18T05:08:09Z", "catching_up": False}
    return {"checked_at_utc": "2026-11-18T05:08:09Z", "round_state": state,
            "validator_set": {"block_height": "10", "total": str(len(validators)), "validators": validators},
            "statuses": [{"source": "a", **status}, {"source": "b", **status}]}


def assess_fixture(snapshot):
    return assess(snapshot, "fixture_990012-1", now=NOW)


class ConsensusHealthTests(unittest.TestCase):
    def test_strict_integer_boundary(self):
        self.assertEqual(required_power(3), 3)
        self.assertEqual(required_power(4), 3)
        self.assertEqual(required_power(21779320), 14519547)
        for value in (True, 0, -1, 2.5, "2.0", "01", "-2"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                required_power(value)

    def test_huge_power_does_not_round_or_overflow(self):
        huge = 10 ** 500
        snapshot = fixture((huge, huge, huge))
        result = assess_fixture(snapshot)
        self.assertTrue(result["valid_snapshot"])
        self.assertEqual(result["round_observations"]["required_power"], 2 * huge + 1)
        self.assertEqual(result["round_observations"]["prevotes"]["observed_percent"], "100.0000")

    def test_decimal_stalled_observation(self):
        snapshot = fixture((5786843, 2021732, 1464429, 12506316))
        rounds = snapshot["round_state"]["height_vote_set"][0]
        rounds["prevotes"][:3] = ["nil-Vote"] * 3
        rounds["precommits"] = ["nil-Vote"] * 4
        for status in snapshot["statuses"]:
            status["block_time"] = "2026-11-15T11:30:49Z"
        result = assess_fixture(snapshot)
        prevotes = result["round_observations"]["prevotes"]
        self.assertEqual(prevotes["observed_power_matching_proposal_prefix"], 12506316)
        self.assertEqual(prevotes["missing_power_to_threshold"], 2013231)
        self.assertEqual(prevotes["observed_percent"], "57.4229")
        self.assertTrue(result["pause_new_bridge_processing"])
        self.assertEqual(result["round_observations"]["precommits"]["observed_power_matching_proposal_prefix"], 0)

    def test_failures_weighted_by_group(self):
        snapshot = fixture()
        a, b = (v["address"] for v in snapshot["validator_set"]["validators"][:2])
        snapshot["failure_groups"] = {"one_operator": [a], "shared_cloud_account": [a, b]}
        result = assess_fixture(snapshot)
        self.assertTrue(result["failure_groups"]["one_operator"]["survives_group_failure"])
        self.assertFalse(result["failure_groups"]["shared_cloud_account"]["survives_group_failure"])
        self.assertTrue(result["operator_diversity_evaluated"])

    def test_exact_third_lost_does_not_survive(self):
        snapshot = fixture((1, 1, 1))
        snapshot["failure_groups"] = {"one": [snapshot["validator_set"]["validators"][0]["address"]]}
        self.assertFalse(assess_fixture(snapshot)["failure_groups"]["one"]["survives_group_failure"])

    def test_unprovided_groups_not_assumed_independent(self):
        self.assertFalse(assess_fixture(fixture())["operator_diversity_evaluated"])

    def test_unknown_or_repeated_group_member_invalid(self):
        for members in (["F" * 40], ["000000000001" + "A" * 28] * 2):
            snapshot = fixture()
            snapshot["failure_groups"] = {"operator": members}
            self.assertFalse(assess_fixture(snapshot)["valid_snapshot"])

    def test_votes_not_servers_and_not_stages(self):
        snapshot = fixture((90, 3, 3, 4))
        snapshot["round_state"]["height_vote_set"][0]["prevotes"][0] = "nil-Vote"
        snapshot["round_state"]["height_vote_set"][0]["precommits"] = ["nil-Vote"] * 4
        result = assess_fixture(snapshot)
        self.assertFalse(result["round_observations"]["prevotes"]["observation_reaches_threshold"])
        self.assertEqual(result["round_observations"]["precommits"]["observed_power_matching_proposal_prefix"], 0)

    def test_different_blocks_not_aggregated(self):
        snapshot = fixture()
        validators = snapshot["validator_set"]["validators"]
        votes = snapshot["round_state"]["height_vote_set"][0]["prevotes"]
        votes[2:] = [vote(i, validators[i], block="E" * 12) for i in (2, 3)]
        row = assess_fixture(snapshot)["round_observations"]["prevotes"]
        self.assertEqual(row["observed_other_block_power"], 2)
        self.assertFalse(row["observation_reaches_threshold"])

    def test_nil_block_is_a_vote_not_missing(self):
        snapshot = fixture()
        snapshot["round_state"]["height_vote_set"][0]["prevotes"][0] = vote(
            0, snapshot["validator_set"]["validators"][0], block="0" * 12)
        row = assess_fixture(snapshot)["round_observations"]["prevotes"]
        self.assertEqual(row["observed_nil_block_power"], 1)
        self.assertEqual(row["missing_votes"], [])

    def test_wrong_vote_context_invalid(self):
        snapshot = fixture()
        validators = snapshot["validator_set"]["validators"]
        for text in (vote(0, validators[0], height=9), vote(0, validators[0], round_number=1),
                     vote(0, validators[0], kind="precommits"), vote(1, validators[1])):
            changed = copy.deepcopy(snapshot)
            changed["round_state"]["height_vote_set"][0]["prevotes"][0] = text
            result = assess_fixture(changed)
            self.assertFalse(result["valid_snapshot"])
            self.assertTrue(result["pause_new_bridge_processing"])

    def test_duplicate_validator_partial_set_and_wrong_height_invalid(self):
        for mode in ("duplicate", "partial", "height", "float_power"):
            snapshot = fixture()
            rows = snapshot["validator_set"]["validators"]
            if mode == "duplicate":
                rows[1] = copy.deepcopy(rows[0])
            elif mode == "partial":
                rows.pop()
            elif mode == "height":
                snapshot["validator_set"]["block_height"] = "9"
            else:
                rows[0]["voting_power"] = 2.5
            self.assertFalse(assess_fixture(snapshot)["valid_snapshot"])

    def test_ambiguous_abbreviated_identity_invalid(self):
        snapshot = fixture()
        snapshot["validator_set"]["validators"][1]["address"] = "000000000001" + "B" * 28
        self.assertFalse(assess_fixture(snapshot)["valid_snapshot"])

    def test_wrong_chain_duplicate_rpc_or_missing_source_invalid(self):
        for field, value in (("chain_id", "other"), ("source", "a"), ("source", "")):
            snapshot = fixture()
            snapshot["statuses"][1][field] = value
            self.assertFalse(assess_fixture(snapshot)["valid_snapshot"])

    def test_stale_snapshot_block_or_future_time_pause(self):
        for mode in ("snapshot", "block", "future", "catching_up"):
            snapshot = fixture()
            if mode == "snapshot":
                snapshot["checked_at_utc"] = "2026-11-15T00:00:00Z"
            elif mode == "block":
                snapshot["statuses"][1]["block_time"] = "2026-11-15T00:00:00Z"
            elif mode == "future":
                snapshot["statuses"][1]["block_time"] = "2026-11-19T00:00:00Z"
            else:
                snapshot["statuses"][1]["catching_up"] = True
            self.assertTrue(assess_fixture(snapshot)["pause_new_bridge_processing"])

    def test_disagreeing_tip_or_round_height_pauses(self):
        for field, value in (("block_hash", "E" * 64), ("height", 8)):
            snapshot = fixture()
            snapshot["statuses"][1][field] = value
            self.assertTrue(assess_fixture(snapshot)["pause_new_bridge_processing"])

    def test_rpc_agreement_never_authorizes_bridge(self):
        result = assess_fixture(fixture())
        self.assertTrue(result["valid_snapshot"])
        self.assertFalse(result["pause_new_bridge_processing"])
        self.assertFalse(result["bridge_execution_authorized"])
        self.assertFalse(result["cryptographically_verified"])

    def test_malformed_snapshot_fail_closed(self):
        for snapshot in (None, [], {}, {"raw": None}, {"checked_at_utc": 5}):
            result = assess_fixture(snapshot)
            self.assertFalse(result["valid_snapshot"])
            self.assertTrue(result["pause_new_bridge_processing"])
            self.assertFalse(result["bridge_execution_authorized"])

    def test_json_duplicate_keys_nan_and_size_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "snapshot.json"
            for raw in (b'{"a":1,"a":2}', b'{"a":NaN}', b"x" * (8 * 1024 * 1024 + 1)):
                path.write_bytes(raw)
                with self.assertRaises(ValueError):
                    load_snapshot(path)

    def test_cli_exit_status_and_local_input_only(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "snapshot.json"
            path.write_text(json.dumps(fixture()), encoding="utf-8")
            arguments = ["--snapshot", str(path), "--chain-id", "fixture_990012-1", "--now", NOW.isoformat()]
            with contextlib.redirect_stdout(io.StringIO()) as out:
                self.assertEqual(main(arguments), 0)
            self.assertFalse(json.loads(out.getvalue())["bridge_execution_authorized"])
            path.write_text("[]", encoding="utf-8")
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(main(arguments), 2)

    def test_cli_deeply_nested_json_returns_structured_pause(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "snapshot.json"
            depth = sys.getrecursionlimit() + 100
            path.write_text('{"extra":' + '[' * depth + '0' + ']' * depth + '}', encoding="utf-8")
            with contextlib.redirect_stdout(io.StringIO()) as output:
                code = main(["--snapshot", str(path), "--chain-id", "fixture_990012-1"])
            self.assertEqual(code, 2)
            result = json.loads(output.getvalue())
            self.assertFalse(result["valid_snapshot"])
            self.assertTrue(result["pause_new_bridge_processing"])
            self.assertFalse(result["bridge_execution_authorized"])

    def test_old_collector_evidence_shape(self):
        snapshot = fixture()
        raw_statuses = {}
        for key, row in zip(("main_status", "node1_status"), snapshot["statuses"]):
            raw_statuses[key] = {"result": {"node_info": {"network": row["chain_id"]}, "sync_info": {
                "latest_block_height": str(row["height"]), "latest_block_hash": row["block_hash"],
                "latest_block_time": row["block_time"], "catching_up": row["catching_up"]}}}
        evidence = {"checked_at_utc": snapshot["checked_at_utc"], "raw": {**raw_statuses,
            "consensus": {"result": {"round_state": snapshot["round_state"]}},
            "validators": {"result": snapshot["validator_set"]}}}
        self.assertTrue(assess_fixture(evidence)["valid_snapshot"])


if __name__ == "__main__":
    unittest.main()
