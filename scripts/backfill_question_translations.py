"""Regenerate migrations/000044_question_translations_backfill.up.sql.

Usage:
    python3 scripts/backfill_question_translations.py union.json

union.json maps Russian source text -> {"kk": ..., "en": ...}. Build it from
the reviewed frontend i18n batches (hints/explanations only, no UI chrome).
Each statement matches source text exactly; rows without a match insert
nothing; ON CONFLICT refreshes only the matched field and preserves the other
field. Dollar-quoting ($tr$) keeps quotes/entities intact.
"""
import json
import sys

TAG = "$tr$"


def q(s: str) -> str:
    assert TAG not in s, f"dollar tag collision in {s[:60]!r}"
    return f"{TAG}{s}{TAG}"


def main() -> None:
    union = json.load(open(sys.argv[1]))
    stmts = []
    for skill, table in (("reading", "reading_questions"), ("listening", "listening_questions")):
        for column in ("explanation", "hint"):
            col_expr = "q.explanation" if column == "explanation" else "q.content->>'hint'"
            values = "v.tr, ''" if column == "explanation" else "'', v.tr"
            for lang in ("kk", "en"):
                rows = [
                    f"({q(ru)}, {q(v[lang])})"
                    for ru, v in union.items()
                    if v.get(lang)
                ]
                vals = ",\n  ".join(rows)
                stmts.append(
                    f"INSERT INTO question_translations (question_id, skill, lang, explanation, hint)\n"
                    f"SELECT q.id, '{skill}', '{lang}', {values}\n"
                    f"FROM {table} q\n"
                    f"JOIN (VALUES\n  {vals}\n) AS v(ru, tr) ON {col_expr} = v.ru\n"
                    f"ON CONFLICT (question_id, lang) DO UPDATE SET {column} = EXCLUDED.{column};"
                )
    header = (
        "-- Backfills question_translations from the reviewed RU->KK/EN dictionary.\n"
        "-- Regenerate with: python3 scripts/backfill_question_translations.py union.json\n"
        "-- > migrations/000044_question_translations_backfill.up.sql\n"
    )
    sys.stdout.write(header + "\n\n".join(stmts) + "\n")


if __name__ == "__main__":
    main()
