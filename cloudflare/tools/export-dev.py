#!/usr/bin/env python3
"""Read-only export of the explicitly selected, quiesced IELTS DEV database.

No export runs without --execute --quiesced. Never prints records or credentials.
The remote container names and image ID are pinned; production is not selectable.
"""

import argparse, base64, hashlib, json, os, pathlib, subprocess, tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
SHA = "6a7bca673cc24d57ce1d2a4bb3f96fc255f27206"
IMAGE = "sha256:3cc32d093c0898032ab50a8d62972f606f09f91e4e111661950630cee063edba"
SSH = ["ssh", "-p", "9865", "-o", "BatchMode=yes", "almat@78.40.109.172", "sh", "-s"]


def canonical(value):
    return json.dumps(
        value, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode()


def write_private(path, content):
    with os.fdopen(
        os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb"
    ) as f:
        f.write(content)


def qi(value):
    return '"' + value.replace('"', '""') + '"'


def literal(value):
    return "'" + value.replace("'", "''") + "'"


def export_sql(schema):
    queries = [
        "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TIME ZONE 'UTC';",
        "SELECT json_build_object('kind','schema','tables',(SELECT json_agg(x ORDER BY name) FROM (SELECT c.relname name, json_agg(json_build_object('name',a.attname,'type',format_type(a.atttypid,a.atttypmod)) ORDER BY a.attnum) columns FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='public' AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped GROUP BY c.relname) x));",
    ]
    for t in schema["tables"]:
        values = []
        for c in t["columns"]:
            col = qi(c["name"])
            typ = c["type"]
            if typ == "bytea":
                expr = f"CASE WHEN {col} IS NULL THEN NULL ELSE json_build_object('$bytes',encode({col},'base64')) END"
            elif (
                typ in ("jsonb", "json")
                or typ.endswith("[]")
                or typ == "bigint"
                or typ.startswith("numeric")
            ):
                expr = (
                    f"{col}::text"
                    if not typ.endswith("[]")
                    else f"array_to_json({col})::text"
                )
            elif typ == "double precision":
                expr = f"encode(float8send({col}),'hex')"
            elif typ == "timestamp with time zone":
                expr = f"to_char({col} AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"000Z\"')"
            elif typ == "date":
                expr = f"{col}::text"
            else:
                expr = col
            values.append(expr)
        pk = next(
            c["definition"][len("PRIMARY KEY (") : -1]
            for c in t["constraints"]
            if c["type"] == "p"
        )
        queries.append(
            f"SELECT json_build_object('table',{literal(t['name'])},'values',json_build_array({','.join(values)})) FROM public.{qi(t['name'])} ORDER BY {pk};"
        )
    queries += [
        "SELECT json_build_object('kind','ledger','value',to_jsonb(s)) FROM schema_migrations s ORDER BY to_jsonb(s)::text;",
        "SELECT json_build_object('kind','sequence','name',sequencename,'value',last_value::text) FROM pg_sequences WHERE schemaname='public' ORDER BY sequencename;",
        "COMMIT;",
    ]
    return "\n".join(queries) + "\n"


def export_database(output):
    schema = json.loads((ROOT / "schema/postgres.json").read_text())
    sql = export_sql(schema)
    script = (
        "set -eu\nactual=$(docker inspect ielts-guest-dev-backend-1 --format '{{.Image}}')\n[ \"$actual\" = "
        + literal(IMAGE)
        + " ] || exit 42\ndocker exec -i ielts-guest-dev-postgres-1 sh -c 'exec psql -X -q -v ON_ERROR_STOP=1 -U \"$POSTGRES_USER\" -d \"$POSTGRES_DB\" -At' <<'IAC_DEV_SQL'\n"
        + sql
        + "IAC_DEV_SQL\n"
    )
    # stdin is a file, so a full stdout pipe cannot deadlock delivery of the SQL.
    script_path = output / "query.sh"
    write_private(script_path, script.encode())
    raw_path = output / "postgres.raw.jsonl"
    with (
        open(script_path, "rb") as source,
        os.fdopen(
            os.open(str(raw_path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb"
        ) as target,
    ):
        r = subprocess.run(SSH, stdin=source, stdout=target, stderr=subprocess.PIPE)
    if r.returncode:
        raise RuntimeError("DEV database export failed; no record output is logged")
    finish_database(raw_path, output, schema)
    script_path.unlink()


def read_schema_header(raw):
    # PostgreSQL json_agg may insert real line breaks between catalog rows.
    # psql -At does not turn that JSON value into a single physical line.
    header = bytearray()
    for line in raw:
        header.extend(line)
        if len(header) > 1 << 20:
            raise RuntimeError("PostgreSQL schema header exceeds the bounded limit")
        try:
            value = json.loads(header)
        except json.JSONDecodeError:
            continue
        if not isinstance(value, dict) or value.get("kind") != "schema":
            raise RuntimeError("PostgreSQL schema header is invalid")
        return value
    raise RuntimeError("PostgreSQL schema header is incomplete")


def finish_database(raw_path, output, schema):
    tables = {
        t["name"]: {
            "columns": [c["name"] for c in t["columns"]],
            "count": 0,
            "chain": "0" * 64,
        }
        for t in schema["tables"]
    }
    ledger = []
    sequences = []
    rows_path = output / "rows.jsonl"
    with (
        open(raw_path, "rb") as raw,
        os.fdopen(
            os.open(str(rows_path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb"
        ) as target,
    ):
        actual = read_schema_header(raw)
        expected = {
            t["name"]: [{"name": c["name"], "type": c["type"]} for c in t["columns"]]
            for t in schema["tables"]
        }
        actual_tables = {t["name"]: t["columns"] for t in actual["tables"]}
        if set(actual_tables) != (set(expected) | {"schema_migrations"}) or any(
            actual_tables.get(name) != columns for name, columns in expected.items()
        ):
            raise RuntimeError(
                "Live schema differs from selected DEV migrations; export is not importable"
            )
        for line in raw:
            record = json.loads(line)
            if record.get("kind") == "ledger":
                ledger.append(record["value"])
                continue
            if record.get("kind") == "sequence":
                sequences.append(record)
                continue
            t = tables[record["table"]]
            if len(record["values"]) != len(t["columns"]):
                raise RuntimeError("Export column count mismatch")
            for v in record["values"]:
                if isinstance(v, dict) and "$bytes" in v:
                    v["$bytes"] = "".join(v["$bytes"].split())
            row = canonical(record)
            t["chain"] = hashlib.sha256(
                bytes.fromhex(t["chain"]) + hashlib.sha256(row).digest()
            ).hexdigest()
            t["count"] += 1
            target.write(row + b"\n")
    manifest = {
        "format": "iac-cloudflare-dev-v1",
        "revision": SHA,
        "source_image": IMAGE,
        "tables": tables,
        "ledger": ledger,
        "sequences": sequences,
    }
    write_private(output / "database.json", canonical(manifest) + b"\n")
    raw_path.unlink()
    print(
        "DEV database export complete:",
        len(tables),
        "tables;",
        sum(t["count"] for t in tables.values()),
        "rows. Values are private.",
    )


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--execute", action="store_true")
    p.add_argument(
        "--quiesced",
        action="store_true",
        help="Owner has separately quiesced DEV writes across DB, Redis and objects",
    )
    p.add_argument("--output", type=pathlib.Path, required=True)
    a = p.parse_args()
    if not (a.execute and a.quiesced):
        raise SystemExit(
            "No export executed. Requires explicit --execute --quiesced at the approved cutover."
        )
    output = a.output.resolve()
    private = (ROOT / ".sensitive").resolve()
    if private not in output.parents:
        raise SystemExit("Output must be a new directory under cloudflare/.sensitive/")
    output.mkdir(parents=True, mode=0o700, exist_ok=False)
    os.chmod(output, 0o700)
    export_database(output)
    # Separate read-only helpers preserve objects and Redis; no secret is returned to this process.
    from export_storage import export_storage

    export_storage(output, SSH)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.SubprocessError, OSError, ValueError):
        raise SystemExit(
            "DEV export failed. Incomplete private artifacts retained for inspection; no source data changed."
        )
