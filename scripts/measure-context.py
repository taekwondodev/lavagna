# /// script
# dependencies = ["tiktoken==0.14.0"]
# ///
"""Run with uv run scripts/measure-context.py. No product dependency is added.

Counts authored stdin + command arguments + captured stdout AND stderr with
actual o200k_base tokenization. HTTP traffic and browser rendering are not agent
context. Fixed local fixtures, not a claim about every model or real workload.
"""

import io
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tarfile
import tempfile
import urllib.request

import tiktoken

ROOT = Path(__file__).resolve().parents[1]
BASE = "8181887f0a09f2c152e9fad09bda4103f1a1a6a6"
ENC = tiktoken.get_encoding("o200k_base")
DECIDE = '# Decidere\n## Storage? {id="storage"}\n- [file] Files\n- [db] Database\n'
CONTENT = "# Capire\n" + "\n\n".join(
    f"## Criterion {i}\nA per-session file avoids contention; a database coordinates shared writes."
    for i in range(1, 41)
) + "\n\n"


def command(binary, env, *args):
    result = subprocess.run([str(binary), *args], env=env, capture_output=True, text=True, check=True)
    return result.stdout, result.stderr


def count(text):
    text = re.sub(r"http://127\.0\.0\.1:\d+/s/[0-9a-f]{64}/", "http://127.0.0.1:12345/s/" + "a" * 64 + "/", text)
    return {"bytes": len(text.encode()), "tokens": len(ENC.encode(text))}


def present(binary, env, source, submission, comments, reuse, baseline):
    args = ["round", "--reuse", reuse] if reuse else ["round"]
    proc = subprocess.Popen([str(binary), *args], env=env, stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        proc.stdin.write(source)
        proc.stdin.close()
        proc.stdin = None
        status = (proc.stdout if baseline else proc.stderr).readline()
        url = re.search(r"http://127\.0\.0\.1:\d+/s/[0-9a-f]{64}/", status)
        if not url:
            raise RuntimeError("No round URL: " + status)
        url = url.group()
        with urllib.request.urlopen(url + "events", timeout=10) as stream:
            for line in stream:
                if line.startswith(b"data: "):
                    view = json.loads(line[6:])
                    break
        data = dict(round=view["round"], token=view["token"], submission=submission,
                    choices={"storage": "file"}, comments=[dict(text=text) for text in comments])
        request = urllib.request.Request(url + "send", data=json.dumps(data).encode(),
                    headers={"Origin": url.split("/s/")[0], "Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=10) as reply:
            assert reply.status == 202
        stdout, stderr = proc.communicate(timeout=10)
        if proc.returncode:
            raise RuntimeError(stdout + stderr)
        result = json.loads(stdout.splitlines()[-1])
        context = "lavagna " + " ".join(args) + "\n" + source + status + stdout + stderr
        return context, result
    finally:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        proc.wait()


def main():
    with tempfile.TemporaryDirectory(prefix="lavagna-context-") as directory:
        temp = Path(directory)
        old = temp / "baseline"
        old.mkdir()
        archive = subprocess.check_output(["git", "archive", BASE], cwd=ROOT)
        with tarfile.open(fileobj=io.BytesIO(archive)) as files:
            files.extractall(old, filter="data")
        binaries = [temp / "old", temp / "new"]
        for source, binary in zip([old, ROOT], binaries):
            subprocess.run(["go", "build", "-o", str(binary), "."], cwd=source, check=True)
        results = {}
        for baseline, binary in zip([True, False], binaries):
            home = temp / ("home-old" if baseline else "home-new")
            home.mkdir()
            env = {"PATH": os.environ["PATH"], "HOME": str(home), "BROWSER": "true", "LAVAGNA_SESSION": "measurement"}
            help_out = "lavagna round --help\n" + "".join(command(binary, env, "round", "--help"))
            first, _ = present(binary, env, CONTENT + DECIDE, "s-0000000000000001", [], None, baseline)
            second, _ = present(binary, env, (CONTENT if baseline else "") + DECIDE,
                                "s-0000000000000002", [], None if baseline else "r1", baseline)
            small, _ = present(binary, env, DECIDE, "s-0000000000000003", ["Use files, keep the cleanup explicit."], None, baseline)
            comments = ["Observed write contention; use one file per session. " * 250, "Approved, with daily cleanup."]
            large, result = present(binary, env, DECIDE, "s-0000000000000004", comments, None, baseline)
            measurements = {"default_help": count(help_out), "two_round_comparison": count(first + second),
                            "small_feedback_round": count(small), "large_feedback_round": count(large)}
            if baseline:
                measurements["large_feedback_one_comment"] = count(large)
                measurements["large_feedback_all_comments"] = count(large)
                measurements["large_feedback_paged_all_comments"] = count(large)
            else:
                assert result.get("deferred"), result
                def read(*args):
                    argv = ("feedback", result["submission"], *args)
                    stdout, stderr = command(binary, env, *argv)
                    return "lavagna " + " ".join(argv) + "\n" + stdout + stderr, json.loads(stdout)
                overview, _ = read()
                selected, page = read("--comment", "2")
                assert page["text"] == comments[1] and page["next"] == 0
                measurements["large_feedback_one_comment"] = count(large + overview + selected)
                full = large + overview
                for index, expected in enumerate(comments, 1):
                    offset, text = 0, ""
                    while True:
                        context, page = read("--comment", str(index), "--offset", str(offset))
                        full += context
                        text += page["text"]
                        offset = page["next"]
                        if offset == 0:
                            break
                    assert text == expected
                measurements["large_feedback_paged_all_comments"] = count(full)
                all_context, complete = read("--all")
                assert [comment["text"] for comment in complete["comments"]] == comments
                measurements["large_feedback_all_comments"] = count(large + all_context)
            results["baseline" if baseline else "candidate"] = measurements
        print(json.dumps({"baseline": BASE, "tokenizer": "tiktoken 0.14.0 / o200k_base",
                          "normalization": "Only random capability URLs and ports are fixed; both output streams count.",
                          "measurements": results}, indent=2))


if __name__ == "__main__":
    main()
