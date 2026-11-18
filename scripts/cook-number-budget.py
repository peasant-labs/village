#!/usr/bin/env python3
"""Operator-only transcript budget measurements; never invoke from CI."""
import argparse
import json
import math
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
MARKER = re.compile(r"^mode=(cold|warm) passes=(\d+) samples=(\[.*\])$", re.M)
SUBJECTS = ("ascii", "escaped-multibyte", "cook-number-dense-largest-storable")
LIMIT = 250_000_000


def run(args, *, cwd=ROOT, env=None):
    return subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, check=True).stdout


def pin_at(rev):
    text = run(["git", "show", f"{rev}:backend/go.mod"])
    match = re.search(r"^\s*github.com/peasant-labs/schema\s+(\S+)", text, re.M)
    if not match:
        raise ValueError(f"cannot determine Schema pin at {rev}")
    return match.group(1)


def parse_samples(output, mode, count, runs=1):
    records = []
    markers = MARKER.findall(output)
    if len(markers) != sum(line.startswith("mode=") for line in output.splitlines()):
        raise ValueError("malformed benchmark marker")
    for found_mode, found_count, payload in markers:
        if found_mode != mode:
            raise ValueError("unexpected benchmark mode")
        samples = json.loads(payload)
        if len(samples) != int(found_count):
            raise ValueError("benchmark sample count disagrees with marker")
        # Go calibrates warm benchmarks with one preliminary iteration.
        if mode == "warm" and len(samples) == 1 and count != 1:
            continue
        if len(samples) != count:
            raise ValueError("benchmark sample count differs from request")
        if any(set(s) != {"wall_ns", "cpu_ns", "allocated_bytes", "max_rss_kib"} or
               any(not isinstance(v, int) or v < 0 for v in s.values()) for s in samples):
            raise ValueError("invalid benchmark sample")
        records.append(samples)
    if len(records) != runs:
        raise ValueError(f"expected {runs} {mode} markers, got {len(records)}")
    return records


def percentile(values):
    values = sorted(values)
    return values[math.ceil(len(values) * .95) - 1]

def fit(points):
    xmean = sum(x for x, _ in points) / len(points)
    ymean = sum(y for _, y in points) / len(points)
    denominator = sum((x - xmean) ** 2 for x, _ in points)
    if denominator == 0:
        raise ValueError("cannot fit a curve with equal subject sizes")
    slope = sum((x - xmean) * (y - ymean) for x, y in points) / denominator
    return {"slope": slope, "intercept": ymean - slope * xmean}


def physical_cores():
    data = run(["lscpu", "-p=CPU,CORE,SOCKET,ONLINE"])
    return [tuple(int(x) for x in line.split(",")[:3]) for line in data.splitlines()
            if line and not line.startswith("#") and line.split(",")[3] == "Y"]


def siblings(cores):
    rows = physical_cores()
    selected = [row for row in rows if row[0] in cores]
    if len(selected) != 4 or len({row[1:] for row in selected}) != 4:
        raise ValueError("select exactly four online, distinct physical cores")
    return sorted(row[0] for row in rows if row[1:] in {s[1:] for s in selected} and row[0] not in cores)


def cpu_ticks(ids):
    out = {}
    for line in pathlib.Path("/proc/stat").read_text().splitlines():
        words = line.split()
        if words[0].startswith("cpu") and words[0][3:].isdigit() and int(words[0][3:]) in ids:
            values = list(map(int, words[1:9]))
            out[int(words[0][3:])] = (sum(values), values[3] + values[4])
    if set(out) != set(ids):
        raise ValueError("cannot read every sibling CPU in /proc/stat")
    return out


def utilization(before, after):
    result = {}
    for cpu, (total, idle) in before.items():
        dt, di = after[cpu][0] - total, after[cpu][1] - idle
        if dt <= 0:
            raise ValueError("sibling CPU utilization interval is empty")
        result[str(cpu)] = 100 * (dt - di) / dt
    return result


def governor(cores):
    return {str(c): pathlib.Path(f"/sys/devices/system/cpu/cpu{c}/cpufreq/scaling_governor").read_text().strip()
            for c in cores}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--gate", action="store_true")
    parser.add_argument("--curve", action="store_true")
    parser.add_argument("--content-baseline", action="store_true")
    parser.add_argument("--all-subjects", action="store_true")
    parser.add_argument("--concurrent", type=int, default=2)
    parser.add_argument("--cores", default="0,1,2,3")
    parser.add_argument("--rev")
    parser.add_argument("--expect-fail", action="store_true")
    args = parser.parse_args()
    if os.getenv("CI"):
        raise ValueError("budget measurements are prohibited in CI")
    if not (args.gate or args.curve or args.content_baseline or args.all_subjects):
        raise ValueError("select --gate, --curve, --content-baseline, or --all-subjects")
    if args.expect_fail and not args.gate:
        raise ValueError("--expect-fail requires --gate")
    if args.concurrent != 2:
        raise ValueError("the concurrent comparison requires k=2")
    current = pin_at("HEAD")
    target = pin_at(args.rev or "HEAD")
    if args.expect_fail and (not args.rev or current == target):
        raise ValueError(f"no change under test: current Schema {current}, selected Schema {target}")
    cores = [int(x) for x in args.cores.split(",")]
    sib = siblings(cores)
    allowed = os.sched_getaffinity(0)
    if not set(cores).issubset(allowed):
        raise ValueError("requested cores are outside the process affinity")
    gov = governor(cores)
    if len(set(gov.values())) != 1:
        raise ValueError("selected CPUs use different governors")
    env = dict(os.environ, GOMAXPROCS="4", GOGC="100", GOWORK="off")
    result = {"commit": run(["git", "rev-parse", args.rev or "HEAD"]).strip(), "schema_pin": target,
              "current_schema_pin": current, "go_version": run(["go", "version"]).strip(),
              "gomaxprocs": 4, "gogc": 100, "gomemlimit": env.get("GOMEMLIMIT", "unset"),
              "cores": cores, "siblings": sib, "governor": gov, "subjects": {}}
    before = cpu_ticks(sib)
    with tempfile.TemporaryDirectory(prefix="cook-budget-", dir="/tmp/opencode") as temp:
        workroot = ROOT
        if args.rev:
            workroot = pathlib.Path(temp) / "source"
            workroot.mkdir()
            archive = pathlib.Path(temp) / "source.tar"
            with archive.open("wb") as out:
                subprocess.run(["git", "archive", args.rev], cwd=ROOT, stdout=out, check=True)
            with tarfile.open(archive) as tf:
                tf.extractall(workroot, filter="data")
            for name in ("transcript_cook_budget_test.go", "testdata/cook_number_budget.yaml"):
                relative = pathlib.Path("backend/internal/handler") / name
                (workroot / relative).parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / relative, workroot / relative)
        binary = str(pathlib.Path(temp) / "handler.test")
        run(["go", "test", "-c", "-o", binary, "./internal/handler"], cwd=workroot / "backend", env=env)

        def generate(subject, turns):
            path = str(pathlib.Path(temp) / "input.json")
            e = dict(env, COOK_BUDGET_GENERATE=path, COOK_BUDGET_SUBJECT=subject,
                     COOK_BUDGET_TURNS=str(turns))
            p = subprocess.run([binary, "-test.run=^TestCookBudgetGenerate$", "-test.count=1"],
                               cwd=workroot / "backend", env=e, capture_output=True, text=True)
            if p.returncode:
                if "subject exceeds 127 MiB" in p.stdout:
                    return None
                raise ValueError("subject generation or content-source validation failed: " + p.stdout + p.stderr)
            return path, os.path.getsize(path)

        def command(path, content, cold, warm_count=200, runs=1):
            e = dict(env, COOK_BUDGET_INPUT=path)
            if cold:
                e["COOK_BUDGET_COLD"] = "1"
            else:
                e.pop("COOK_BUDGET_COLD", None)
            name = "BenchmarkContentCold" if content else "BenchmarkCookNumberCold"
            cmd = ["taskset", "-c", ",".join(map(str, cores)), binary, "-test.run=^$",
                   f"-test.bench=^{name}$", f"-test.benchtime={1 if cold else warm_count}x",
                   f"-test.count={runs}"]
            return cmd, e

        def sample(path, content, cold):
            cmd, e = command(path, content, cold, runs=1 if cold else 3)
            return parse_samples(run(cmd, cwd=workroot / "backend", env=e),
                                 "cold" if cold else "warm", 1 if cold else 200,
                                 1 if cold else 3)

        def concurrent(path, content):
            cmd, e = command(path, content, True)
            children = [subprocess.Popen(cmd, cwd=workroot / "backend", env=e, text=True,
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(2)]
            samples = []
            for child in children:
                out, err = child.communicate()
                if child.returncode:
                    raise ValueError("concurrent benchmark failed: " + err + out)
                samples.extend(parse_samples(out, "cold", 1)[0])
            return samples

        for subject in SUBJECTS:
            if subject == SUBJECTS[2]:
                low, high = 1, 131072
                while low + 1 < high:
                    mid = (low + high) // 2
                    if generate(subject, mid):
                        low = mid
                    else:
                        high = mid
                turns = low
            else:
                turns = 100000
            generated = generate(subject, turns)
            if not generated:
                raise ValueError("chosen subject exceeds stored limit")
            path, size = generated
            entry = {"turns": turns, "bytes": size}
            result["subjects"][subject] = entry
            if args.gate or args.all_subjects:
                cold = [[sample(path, False, True)[0][0] for _ in range(40)] for _ in range(3)]
                warm = sample(path, False, False)
                entry["cold_runs"], entry["warm_runs"] = cold, warm
                entry["cold_p95_ns"] = [percentile([s["wall_ns"] for s in r]) for r in cold]
                entry["warm_p95_ns"] = [percentile([s["wall_ns"] for s in r]) for r in warm]
            if args.content_baseline or args.all_subjects:
                paged = sample(path, False, True)[0][0]
                content = sample(path, True, True)[0][0]
                cpaged = concurrent(path, False)
                ccontent = concurrent(path, True)
                entry["comparison"] = {"paged": paged, "content": content,
                                       "concurrent_paged": cpaged, "concurrent_content": ccontent}
        if args.curve or args.all_subjects:
            result["curves"] = {}
            for subject in SUBJECTS[:2]:
                points = []
                for turns in (10000, 20000, 50000, 100000):
                    generated = generate(subject, turns)
                    if not generated:
                        raise ValueError("curve subject exceeds stored limit")
                    path, size = generated
                    cold = [sample(path, False, True)[0][0] for _ in range(40)]
                    points.append({"turns": turns, "bytes": size, "samples": cold,
                                   "p95_ns": percentile([s["wall_ns"] for s in cold])})
                by_turn = fit([(p["turns"], p["p95_ns"]) for p in points])
                by_byte = fit([(p["bytes"], p["p95_ns"]) for p in points])
                result["curves"][subject] = {"points": points, "ns_per_turn": by_turn,
                    "ns_per_byte": by_byte,
                    "turns_at_250ms": (LIMIT - by_turn["intercept"]) / by_turn["slope"] if by_turn["slope"] > 0 else None}
        result["sibling_utilization_percent"] = utilization(before, cpu_ticks(sib))
        result["governor_after"] = governor(cores)
    errors = []
    if result["governor_after"] != gov or any(v > 1 for v in result["sibling_utilization_percent"].values()):
        errors.append("pinning conditions changed or sibling utilization exceeded 1%")
    if args.gate:
        for name, entry in result["subjects"].items():
            if any(x > LIMIT for x in entry["cold_p95_ns"] + entry["warm_p95_ns"]):
                errors.append(f"{name} exceeds the 250 ms p95 budget")
    if args.content_baseline or args.all_subjects:
        for name, entry in result["subjects"].items():
            c = entry["comparison"]
            for field in ("cpu_ns", "allocated_bytes"):
                if c["paged"][field] > c["content"][field]:
                    errors.append(f"{name} single {field} ratio exceeds one")
            for field in ("cpu_ns", "max_rss_kib"):
                aggregate = sum if field == "cpu_ns" else max
                if aggregate(x[field] for x in c["concurrent_paged"]) > aggregate(x[field] for x in c["concurrent_content"]):
                    errors.append(f"{name} concurrent {field} ratio exceeds one")
    result["errors"] = errors
    print(json.dumps(result, indent=2))
    if args.expect_fail:
        return bool(any(not x.endswith("exceeds the 250 ms p95 budget") for x in errors) or not errors)
    return bool(errors)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f"cook-number-budget: {error}", file=sys.stderr)
        sys.exit(1)
