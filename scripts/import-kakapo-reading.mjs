// Import the user-supplied Kakapo / elms / stress document as one 40-question
// Reading material. Dry-run by default. No account creation or material updates.
// Node 22+; run from the backend directory, with the shared-dev backend running.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash, createHmac, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { parseArgs, parseEnv } from 'node:util';

const { values } = parseArgs({ options: {
  source: { type: 'string' }, explanations: { type: 'string' },
  actor: { type: 'string' }, write: { type: 'boolean', default: false },
} });
assert(values.source && values.explanations && values.actor,
  'Required: --source <file> --explanations <file> --actor <existing admin UUID> [--write]');
assert.match(values.actor, /^[0-9a-f-]{36}$/i);

const env = { ...parseEnv(readFileSync('.env', 'utf8')), ...parseEnv(readFileSync('.env.dev', 'utf8')) };
const database = new URL(env.DATABASE_URL);
assert.equal(database.hostname, 'a1-postgres1.alem.ai');
assert.equal(database.port, '30100');
assert.equal(database.pathname, '/ielts');
const runningDatabase = execFileSync('docker', ['exec', 'ielts-shared-dev-backend-1', 'printenv', 'DATABASE_URL'], { encoding: 'utf8' }).trim();
assert(new URL(runningDatabase).href === database.href, 'Running backend points to a different database');
const actorRole = execFileSync('docker', [
  'run', '--rm', '-e', 'PGPASSWORD', '-e', 'PGSSLMODE=disable', '-e', 'PGCONNECT_TIMEOUT=10',
  '-e', 'PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=10000',
  'postgres:17-alpine', 'psql', '-XAtw', '-h', database.hostname, '-p', database.port,
  '-U', decodeURIComponent(database.username), '-d', 'ielts', '-v', 'ON_ERROR_STOP=1',
  '-c', `SELECT role FROM users WHERE id='${values.actor}' AND status='REGISTERED'`,
], { encoding: 'utf8', env: { ...process.env, PGPASSWORD: decodeURIComponent(database.password) } }).trim();
assert.equal(actorRole, 'ADMIN', 'An existing registered administrator must own this import');

const source = readFileSync(values.source, 'utf8').replace(/\r\n/g, '\n').trim();
const explanationSource = readFileSync(values.explanations, 'utf8').replace(/\r\n/g, '\n').trim();
const answers = [
  'FALSE','FALSE','FALSE','NOT_GIVEN','TRUE','TRUE',
  'Bulbs','Soil','Feathers','Deer','1980','Funding','Stakeholders',
  'C','G','B','E','C','B','A','B','C','A','Oak','Flooring','Keel',
  'C','A','D','C','B','G','F','E','D','YES','NOT_GIVEN','NO','YES','YES',
];
assert.equal(answers.length, 40);
const slug = 'reading-kakapo-elms-stress-40';
const title = 'Reading: The Kakapo / To Britain / How stress affects our judgement';

function between(start, end) {
  const a = source.indexOf(start);
  assert(a >= 0, `Missing source marker: ${start}`);
  const b = end ? source.indexOf(end, a + start.length) : source.length;
  assert(b > a, `Missing end marker: ${end}`);
  return source.slice(a + start.length, b).trim().replace(/\n{3,}/g, '\n\n');
}
function questionBlock(start, end, first) {
  const block = between(start, end);
  const split = block.search(new RegExp(`^${first}\\.\\s*`, 'm'));
  assert(split >= 0, `Question ${first} missing`);
  return { instruction: block.slice(0, split).trim(), questions: block.slice(split).trim().replace(/^(\d+)\.(?=\S)/gm, '$1. ') };
}
function group(number, range, type, instruction, contents, extra = '') {
  return `### GROUP ${number}\nrange: ${range}\ntype: ${type}\n${extra}instruction:\n${instruction}\n\n${contents}\n`;
}
function blanks(text) {
  return text.replace(/\((\d+)\)\s*[….]+/g, (_, number) => `{{${number}}}`);
}
const passage1 = between('THE KAKAPO', 'Questions 1-6').replace('Kakap6 breed', 'Kakapo breed');
const passage2 = between('\nTo Britain\n', 'Questions 14-18');
const passage3 = between('How stress affects our judgement', 'Questions 27-30');
for (const text of [passage1, passage2, passage3]) assert(text.length > 1000);

const groups = [];
const tfng = questionBlock('Questions 1-6', 'Questions 7-13', 1);
groups.push(group(1, '1-6', 'TRUE_FALSE_NOT_GIVEN', `Passage 1 — Questions 1–6\n${tfng.instruction}`, tfng.questions));
const notes = between('Questions 7-13', '\nTo Britain\n');
const notesStart = notes.indexOf('New Zealand’s kakapo');
assert(notesStart > 0);
groups.push(group(2, '7-13', 'NOTE_COMPLETION', `Passage 1 — Questions 7–13\n${notes.slice(0, notesStart).trim()}`,
  `content:\n${blanks(notes.slice(notesStart))}`, 'answer_limit: ONE_WORD_AND_OR_NUMBER\n'));

const information = questionBlock('Questions 14-18', 'Questions 19-23', 14);
groups.push(group(3, '14-18', 'MATCHING_INFORMATION', `Passage 2 — Questions 14–18\n${information.instruction}`,
  `options:\n${'ABCDEFG'.split('').map(letter => `${letter}: Section ${letter}`).join('\n')}\n\n${information.questions}`, 'reuse_options: true\n'));
const people = questionBlock('Questions 19-23', 'Questions 24-26', 19);
const [peopleQuestions, peopleOptions] = people.questions.split('List of People');
assert(peopleOptions);
groups.push(group(4, '19-23', 'MATCHING_FEATURES', `Passage 2 — Questions 19–23\n${people.instruction}`,
  `options:\n${peopleOptions.trim()}\n\n${peopleQuestions.trim()}`, 'reuse_options: true\n'));
const summary = between('Questions 24-26', 'How stress affects our judgement');
const summaryStart = summary.indexOf('Uses of a popular tree');
assert(summaryStart > 0);
// V1 needs one numbered blank per question line; keep the complete summary as context.
const summaryContent = blanks(summary.slice(summaryStart)).replace(/ Starting in the Bronze Age/, '\nStarting in the Bronze Age').replace(/ Due to its strength/, '\nDue to its strength');
groups.push(group(5, '24-26', 'SUMMARY_COMPLETION', `Passage 2 — Questions 24–26\n${summary.slice(0, summaryStart).trim()}`,
  `content:\n${summaryContent}`, 'answer_limit: ONE_WORD_ONLY\n'));
const choice = questionBlock('Questions 27-30', 'Questions 31-35', 27);
groups.push(group(6, '27-30', 'MULTIPLE_CHOICE', `Passage 3 — Questions 27–30\n${choice.instruction}`, choice.questions));
const endings = questionBlock('Questions 31-35', 'Questions 36-40', 31);
const endingsStart = endings.questions.search(/^A\./m);
assert(endingsStart > 0);
groups.push(group(7, '31-35', 'MATCHING_SENTENCE_ENDINGS', `Passage 3 — Questions 31–35\n${endings.instruction}`,
  `options:\n${endings.questions.slice(endingsStart)}\n\n${endings.questions.slice(0, endingsStart).trim()}`));
const ynng = questionBlock('Questions 36-40', null, 36);
groups.push(group(8, '36-40', 'YES_NO_NOT_GIVEN', `Passage 3 — Questions 36–40\n${ynng.instruction}`, ynng.questions));

const explanations = [...explanationSource.matchAll(/^Question (\d+)\.\s*([\s\S]*?)(?=^Question \d+(?:\.|-)|$(?![\s\S]))/gm)]
  .map(match => [Number(match[1]), match[2].trim()]);
assert.deepEqual(explanations.map(([n]) => n), Array.from({ length: 13 }, (_, i) => i + 1));
explanations[12][1] = explanations[12][1].replace(/our answer is stakeholder\./g, 'our answer is stakeholders.');
assert(!explanations.some(([, text]) => text.includes('link to the lesson')));

const canonical = [
  '# IELTS_READING_IMPORT_V1', `title: ${title}`, 'exam_type: ACADEMIC', 'duration_minutes: 60',
  '', '## PASSAGE 1', `title: ${title}`, '### TEXT',
  `READING PASSAGE 1 — THE KAKAPO\n\n${passage1}\n\nREADING PASSAGE 2 — TO BRITAIN\n\n${passage2}\n\nREADING PASSAGE 3 — HOW STRESS AFFECTS OUR JUDGEMENT\n\n${passage3}`,
  '', ...groups, '## ANSWERS', ...answers.map((answer, i) => `${i + 1}: ${answer}`), '',
  '## EXPLANATIONS', ...explanations.map(([number, text]) => `### ${number}\n${text}\n`),
].join('\n');

const now = Math.floor(Date.now() / 1000);
function token(role, sub) {
  const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url');
  const body = encode({ alg: 'HS256', typ: 'JWT' }) + '.' + encode({
    sub, role, iss: env.JWT_ISSUER || 'ielts-api', aud: [env.JWT_AUDIENCE || 'ielts-web'],
    iat: now, nbf: now, exp: now + 300, jti: randomUUID(),
  });
  return body + '.' + createHmac('sha256', env.JWT_SECRET).update(body).digest('base64url');
}
const adminToken = token('ADMIN', values.actor);
const studentToken = token('STUDENT', randomUUID());
async function api(path, method = 'GET', body, bearer = adminToken) {
  const response = await fetch('http://localhost:8080/api/v1/' + path, {
    method, headers: { Authorization: 'Bearer ' + bearer, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(60000),
  });
  const data = await response.json();
  assert(response.ok, `${method} ${path}: ${response.status} ${JSON.stringify(data)}`);
  return data;
}
const parsed = await api('admin/reading/import/parse', 'POST', { source: canonical, examType: 'academic', difficulty: 'intermediate' });
assert.deepEqual(parsed.errors, [], JSON.stringify(parsed.errors));
assert.deepEqual(parsed.warnings, [], JSON.stringify(parsed.warnings));
assert.equal(parsed.passages.length, 1);
const material = parsed.passages[0].material;
material.slug = slug;
material.description = 'Academic Reading: three passages, 40 questions. Questions 1–13 include supplied explanations. Suggested time: 60 minutes.';
material.sourceTitle = 'User-provided passages, questions, answer key and explanations';
material.sourceUrl = null;
const questions = material.questionGroups.flatMap(group => group.questions);
assert.equal(material.questionGroups.length, 8);
assert.equal(questions.length, 40);
assert.deepEqual(questions.map(q => q.content.number), Array.from({ length: 40 }, (_, i) => i + 1));
assert.equal(questions.filter(q => q.explanation?.trim()).length, 13);
for (const [i, q] of questions.entries()) {
  assert.equal(q.points, 1);
  const actual = q.answer.value ?? q.answer.optionId ?? q.answer.accepted?.[0];
  assert.equal(actual.toLowerCase(), answers[i].toLowerCase(), `Answer ${i + 1} mismatch`);
}
// The current UI renders context literally; make the numbered blanks readable.
for (const q of questions) {
  if (q.content.context) q.content.context = q.content.context.replace(/\{\{(\d+)\}\}/g, '($1) _____');
}
const checksum = createHash('sha256').update(canonical).digest('hex');
console.log(JSON.stringify({ mode: values.write ? 'write' : 'dry-run', slug, passages: 3, groups: 8, questions: 40, explanations: 13, sha256: checksum }));
if (!values.write) process.exit(0);

const catalog = await api('admin/reading/materials');
const existing = catalog.items.find(item => item.slug === slug);
let saved;
if (existing) {
  saved = await api(`admin/reading/materials/${existing.id}`);
  function comparable(item) {
    return { title: item.title, body: item.body, examType: item.examType, difficulty: item.difficulty,
      questionGroups: item.questionGroups.map(g => ({ type: g.type, instructions: g.instructions,
        questions: g.questions.map(q => ({ prompt: q.prompt, content: q.content, answer: q.answer, explanation: q.explanation, points: q.points })) })) };
  }
  assert.deepEqual(comparable(saved), comparable(material), 'Existing slug differs; refusing to overwrite team content');
  console.log('Existing identical material found; no new version or duplicate created.');
} else {
  saved = await api('admin/reading/materials', 'POST', material);
}
if (saved.status === 'DRAFT') {
  saved = await api(`admin/reading/materials/${saved.id}/publish`, 'POST', { revision: saved.revision });
}
assert.equal(saved.status, 'PUBLISHED');
const stored = await api(`admin/reading/materials/${saved.id}`);
const storedQuestions = stored.questionGroups.flatMap(group => group.questions);
assert.equal(storedQuestions.length, 40);
assert.equal(new Set(storedQuestions.map(q => q.id)).size, 40);
for (const [i, q] of storedQuestions.entries()) {
  assert.deepEqual(q.answer, questions[i].answer);
  assert.equal(q.explanation, questions[i].explanation);
  assert.equal(q.content.number, i + 1);
}
const publicMaterial = await api(`reading/materials/${saved.id}`, 'GET', undefined, studentToken);
assert.equal(publicMaterial.body, material.body);
assert.equal(publicMaterial.questionGroups.flatMap(g => g.questions).length, 40);
for (const q of publicMaterial.questionGroups.flatMap(g => g.questions)) {
  assert(!Object.hasOwn(q, 'answer'));
  assert(!Object.hasOwn(q, 'explanation'));
}
console.log(JSON.stringify({ id: saved.id, slug, status: saved.status, questions: 40, explanations: 13, publicAnswersHidden: true }));
