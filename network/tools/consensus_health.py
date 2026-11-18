"""Offline, read-only consensus diagnostics. Never a finality verifier.

Accepts collector evidence with a ``raw`` object or a compact snapshot with
checked_at_utc, round_state, validator_set, statuses and optional failure_groups.
RPC vote strings abbreviate hashes and signatures; all reported weights are
untrusted observations. No result authorizes mint, release, slash or consensus
changes. This tool has no network, signing, process-control or broadcast code.
"""

import argparse
import datetime
import json
from pathlib import Path
import re
import sys


MAX_SNAPSHOT_BYTES = 8 * 1024 * 1024
_ADDRESS = re.compile(r"[0-9A-F]{40}")
_HASH = re.compile(r"[0-9A-F]{64}")
_VOTE = re.compile(
    r"Vote\{(?P<index>[0-9]+):(?P<address>[0-9A-F]{12}) "
    r"(?P<height>[0-9]+)/(?P<round>[0-9]+)/"
    r"SIGNED_MSG_TYPE_(?P<kind>PREVOTE|PRECOMMIT)\((?P<title>Prevote|Precommit)\) "
    r"(?P<block>[0-9A-F]{12}) [0-9A-F]{12} @ [^}]+\}"
)


def integer(value, minimum=0):
    if type(value) is int:
        result = value
    elif isinstance(value, str) and re.fullmatch(r"0|[1-9][0-9]*", value):
        result = int(value)
    else:
        raise ValueError("expected an integer or canonical decimal integer string")
    if result < minimum:
        raise ValueError("integer below permitted minimum")
    return result


def required_power(total):
    return integer(total, 1) * 2 // 3 + 1


def utc_time(text):
    if not isinstance(text, str):
        raise ValueError("timestamp must be an ISO string with timezone")
    result = datetime.datetime.fromisoformat(text.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("timestamp timezone is required")
    return result.astimezone(datetime.timezone.utc)


def address(value):
    if not isinstance(value, str) or not _ADDRESS.fullmatch(value.upper()):
        raise ValueError("invalid consensus address")
    return value.upper()


def non_nil_hash(value):
    if not isinstance(value, str) or not _HASH.fullmatch(value.upper()) or set(value) == {"0"}:
        raise ValueError("expected a full non-nil block hash")
    return value.upper()


def optional_proposal_hash(value):
    # Comet may have no proposal in a valid current round, especially during
    # quorum loss. Only its explicit nil representations are admitted here;
    # status/committed hashes still require a full, non-nil value.
    if value is None or value == "" or value == "0" * 64:
        return None
    return non_nil_hash(value)


def validator_powers(validator_set, height):
    if integer(validator_set["block_height"], 1) != height:
        raise ValueError("validator set does not belong to the observed consensus height")
    rows = validator_set["validators"]
    if not isinstance(rows, list) or not rows or len(rows) != integer(validator_set["total"], 1):
        raise ValueError("incomplete validator set; fetch all pages")
    powers = {}
    for row in rows:
        identity = address(row["address"])
        if identity in powers:
            raise ValueError("duplicate validator")
        powers[identity] = integer(row["voting_power"], 1)
    if len({identity[:12] for identity in powers}) != len(powers):
        raise ValueError("ambiguous abbreviated validator identity")
    return powers


def observe_round(round_state, validator_set):
    location = round_state["height/round/step"]
    if not isinstance(location, str) or not re.fullmatch(r"[0-9]+/[0-9]+/[0-9]+", location):
        raise ValueError("invalid consensus location")
    height, round_number, step = map(int, location.split("/"))
    if height < 1 or not 1 <= step <= 8:
        raise ValueError("invalid consensus height or step")
    powers = validator_powers(validator_set, height)
    proposal = optional_proposal_hash(round_state["proposal_block_hash"])
    current = [row for row in round_state["height_vote_set"] if integer(row["round"]) == round_number]
    if len(current) != 1:
        raise ValueError("missing or ambiguous current round")
    total = sum(powers.values())
    threshold = required_power(total)
    result = {"height": height, "round": round_number, "step": step,
              "proposal_block_hash": proposal, "proposal_observed": proposal is not None, "total_power": total,
              "required_power": threshold, "validator_count": len(powers),
              "scope": "unverified abbreviated RPC vote observations"}
    for kind, title in (("prevotes", "Prevote"), ("precommits", "Precommit")):
        votes = current[0][kind]
        if not isinstance(votes, list) or len(votes) != len(powers):
            raise ValueError("vote slots differ from validator set")
        matching = nil = other = 0
        missing = []
        for index, ((identity, power), text) in enumerate(zip(powers.items(), votes)):
            if text == "nil-Vote":
                missing.append({"address": identity, "power": power})
                continue
            match = _VOTE.fullmatch(text) if isinstance(text, str) else None
            if not match or int(match["index"]) != index or match["address"] != identity[:12]:
                raise ValueError("invalid vote slot or validator identity")
            if int(match["height"]) != height or int(match["round"]) != round_number:
                raise ValueError("vote height or round mismatch")
            if match["kind"] != kind[:-1].upper() or match["title"] != title:
                raise ValueError("vote consensus stage mismatch")
            if set(match["block"]) == {"0"}:
                nil += power
            elif proposal is not None and proposal.startswith(match["block"]):
                matching += power
            else:
                other += power
        scaled_percent = (matching * 1_000_000 + total // 2) // total
        result[kind] = {"observed_power_matching_proposal_prefix": matching,
                        "observed_percent": f"{scaled_percent // 10000}.{scaled_percent % 10000:04d}",
                        "observation_reaches_threshold": matching >= threshold,
                        "missing_power_to_threshold": max(0, threshold - matching),
                        "observed_nil_block_power": nil, "observed_other_block_power": other,
                        "missing_votes": missing}
    return result


def failure_group_report(powers, groups):
    if not isinstance(groups, dict):
        raise ValueError("failure_groups must map group names to consensus addresses")
    total = sum(powers.values())
    result = {}
    for name, members in groups.items():
        if not isinstance(name, str) or not name or not isinstance(members, list) or not members:
            raise ValueError("invalid failure group")
        identities = [address(member) for member in members]
        if len(set(identities)) != len(identities) or not set(identities) <= powers.keys():
            raise ValueError("duplicate or unknown failure-group member")
        lost = sum(powers[member] for member in identities)
        remaining = total - lost
        result[name] = {"lost_power": lost, "remaining_power": remaining,
                        "survives_group_failure": remaining >= required_power(total)}
    return result


def normalize(snapshot):
    if "raw" not in snapshot:
        return snapshot
    raw = snapshot["raw"]
    statuses = []
    for source in ("main_status", "node1_status", "node2_status"):
        if source not in raw or "result" not in raw[source]:
            continue
        row = raw[source]["result"]
        info = row["sync_info"]
        statuses.append({"source": source, "chain_id": row["node_info"]["network"],
                         "height": info["latest_block_height"], "block_hash": info["latest_block_hash"],
                         "block_time": info["latest_block_time"], "catching_up": info["catching_up"]})
    return {"checked_at_utc": snapshot["checked_at_utc"], "statuses": statuses,
            "round_state": raw["consensus"]["result"]["round_state"],
            "validator_set": raw["validators"]["result"],
            "failure_groups": snapshot.get("failure_groups", {})}


def assess(snapshot, chain_id, now=None, max_age_seconds=120, max_snapshot_age_seconds=60):
    """Return conservative diagnostics. Invalid snapshots always recommend pause."""
    result = {"schema_version": 1, "cryptographically_verified": False,
              "bridge_execution_authorized": False, "pause_new_bridge_processing": True,
              "action_scope": "monitor recommendation only; no chain or contract state is changed"}
    try:
        max_age_seconds = integer(max_age_seconds, 1)
        max_snapshot_age_seconds = integer(max_snapshot_age_seconds, 1)
        if not isinstance(chain_id, str) or not chain_id:
            raise ValueError("expected chain ID is required")
        now = now or datetime.datetime.now(datetime.timezone.utc)
        if now.tzinfo is None:
            raise ValueError("now must include a timezone")
        snapshot = normalize(snapshot)
        checked = utc_time(snapshot["checked_at_utc"])
        age = (now - checked).total_seconds()
        reasons = []
        if age < -5 or age > max_snapshot_age_seconds:
            reasons.append("stale or future-dated snapshot")
        statuses = snapshot["statuses"]
        if not isinstance(statuses, list) or len(statuses) < 2:
            raise ValueError("at least two distinct RPC observations are required")
        sources, tips = set(), set()
        for row in statuses:
            source = row["source"]
            if not isinstance(source, str) or not source or source in sources:
                raise ValueError("missing or duplicate RPC source identity")
            sources.add(source)
            if row["chain_id"] != chain_id:
                raise ValueError("unexpected source chain ID")
            block_time = utc_time(row["block_time"])
            block_age = (now - block_time).total_seconds()
            if block_age < -5 or block_age > max_age_seconds:
                reasons.append("stale or future-dated source block")
            if row["catching_up"] is not False:
                reasons.append("RPC catching up or incomplete")
            tips.add((integer(row["height"], 1), non_nil_hash(row["block_hash"]), block_time))
        if len(tips) != 1:
            reasons.append("RPC tip observations disagree")
        observations = observe_round(snapshot["round_state"], snapshot["validator_set"])
        if any(tip[0] + 1 != observations["height"] for tip in tips):
            reasons.append("consensus height differs from RPC tips")
        powers = validator_powers(snapshot["validator_set"], observations["height"])
        result.update(valid_snapshot=True, reasons=sorted(set(reasons)), round_observations=observations,
                      failure_groups=failure_group_report(powers, snapshot.get("failure_groups", {})),
                      operator_diversity_evaluated=bool(snapshot.get("failure_groups")),
                      pause_new_bridge_processing=bool(reasons))
        result["notice"] = ("Fresh matching RPC observations never prove finality or current signer availability. "
                            "Source identities and failure-group membership are supplied, not independently verified.")
    except (KeyError, TypeError, ValueError, OverflowError, AttributeError) as error:
        result.update(valid_snapshot=False, reasons=[f"invalid snapshot: {error}"])
    return result


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def reject_constant(value):
    raise ValueError("non-finite JSON numbers are not permitted")


def load_snapshot(path):
    with Path(path).open("rb") as stream:
        raw = stream.read(MAX_SNAPSHOT_BYTES + 1)
    if len(raw) > MAX_SNAPSHOT_BYTES:
        raise ValueError("snapshot exceeds size limit")
    return json.loads(raw.decode("utf-8-sig"), object_pairs_hook=unique_object, parse_constant=reject_constant)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--snapshot", required=True, type=Path)
    parser.add_argument("--chain-id", required=True)
    parser.add_argument("--max-age-seconds", type=int, default=120)
    parser.add_argument("--max-snapshot-age-seconds", type=int, default=60)
    parser.add_argument("--now", help="timezone-aware evaluation time, for reproducible offline review")
    args = parser.parse_args(argv)
    try:
        result = assess(load_snapshot(args.snapshot), args.chain_id,
                        now=utc_time(args.now) if args.now else None,
                        max_age_seconds=args.max_age_seconds,
                        max_snapshot_age_seconds=args.max_snapshot_age_seconds)
    except (OSError, ValueError, UnicodeError, RecursionError) as error:
        result = {"valid_snapshot": False, "cryptographically_verified": False,
                  "bridge_execution_authorized": False, "pause_new_bridge_processing": True,
                  "reasons": [f"invalid snapshot: {error}"]}
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 2 if not result["valid_snapshot"] else int(result["pause_new_bridge_processing"])


if __name__ == "__main__":
    sys.exit(main())
