#!/usr/bin/env python3
"""按应用 ApplyMigrations 的语义补齐缺失迁移：单文件单事务、先取 advisory xact lock、同事务写账本。
checksum = sha256(trim(content))。只执行账本里缺失的文件，已有文件 checksum 不符即中止。
用法: expand_migrations.py <migrations_dir> <psql 前缀命令...>   （默认 dry-run，加 --apply 执行）"""
import hashlib, subprocess, sys, pathlib, time
apply = '--apply' in sys.argv
args = [a for a in sys.argv[1:] if a != '--apply']
mdir, psql = pathlib.Path(args[0]), args[1:]
LOCK = 694208311321144027
def q(sql):
    return subprocess.run(psql + ['-At', '-v', 'ON_ERROR_STOP=1', '-f', '-'], input=sql, text=True, capture_output=True, check=True).stdout
live = dict(l.split('|', 1) for l in q('select filename||\'|\'||checksum from schema_migrations;').split())
files = sorted(p for p in mdir.glob('*.sql'))
todo = []
for p in files:
    c = p.read_text().strip()
    if not c: continue
    s = hashlib.sha256(c.encode()).hexdigest()
    if p.name in live:
        if live[p.name] != s: sys.exit(f'ABORT checksum mismatch {p.name}')
        continue
    todo.append((p.name, c, s))
extra = set(live) - {p.name for p in files}
print(f'live={len(live)} files={len(files)} missing={len(todo)} unknown_in_ledger={sorted(extra)}')
for n, _, _ in todo: print('  pending', n)
if not apply: sys.exit(0)
for n, c, s in todo:
    if '$EXPAND$' in c: sys.exit(f'ABORT dollar-quote collision {n}')
    t = time.time()
    q(f"BEGIN;\nSELECT pg_advisory_xact_lock({LOCK});\n{c}\n;\nINSERT INTO schema_migrations(filename, checksum) VALUES ($EXPAND${n}$EXPAND$, '{s}');\nCOMMIT;\n")
    print(f'  applied {n} {time.time()-t:.2f}s')
