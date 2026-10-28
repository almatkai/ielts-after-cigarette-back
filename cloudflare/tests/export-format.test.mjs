import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";

test("PostgreSQL multiline catalog header preserves exact row framing and hashes", () => {
  const code = String.raw`
import importlib.util,json,tempfile,pathlib,hashlib
spec=importlib.util.spec_from_file_location('export_dev','tools/export-dev.py');mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(mod)
schema={'tables':[{'name':'rows','columns':[{'name':'id','type':'text'},{'name':'value','type':'text'}]}]}
header={'kind':'schema','tables':[{'name':'rows','columns':schema['tables'][0]['columns']},{'name':'schema_migrations','columns':[]}]}
record={'table':'rows','values':['one','Unicode Ә with escaped\nnewline']}
for pretty in (False,True):
 with tempfile.TemporaryDirectory() as d:
  p=pathlib.Path(d);raw=p/'raw.jsonl';raw.write_bytes(json.dumps(header,indent=2 if pretty else None).encode()+b'\n'+mod.canonical(record)+b'\n')
  mod.finish_database(raw,p,schema)
  assert (p/'rows.jsonl').read_bytes()==mod.canonical(record)+b'\n'
  manifest=json.loads((p/'database.json').read_text());assert manifest['tables']['rows']['count']==1
  expected=hashlib.sha256(bytes(32)+hashlib.sha256(mod.canonical(record)).digest()).hexdigest()
  assert manifest['tables']['rows']['chain']==expected
from io import BytesIO
for bad in (b'',b'{',b'[]\n',b'{'+b' '*1048576):
 try:mod.read_schema_header(BytesIO(bad))
 except RuntimeError:pass
 else:raise AssertionError('Invalid metadata accepted')
`;
  const result = spawnSync("python3", ["-c", code], {
    cwd: new URL("..", import.meta.url),
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
});
