#!/usr/bin/env python3
import argparse, csv, io, json, subprocess, sys
from decimal import Decimal

C="newapi-postgres"; U="newapi"; Q=Decimal("500000")

def psql(db, sql, ro=True, inp=None):
    cmd=["docker","exec","-i"]
    if ro: cmd += ["-e","PGOPTIONS=-c default_transaction_read_only=on"]
    cmd += [C,"psql","-X","-v","ON_ERROR_STOP=1","-U",U,"-d",db,"-At"]
    cmd += (["-c",sql] if inp is None else ["-f","-"])
    r=subprocess.run(cmd,input=inp,capture_output=True)
    if r.returncode:
        sys.stderr.write(r.stderr.decode("utf-8","replace")); raise SystemExit(r.returncode)
    return r.stdout.decode()

def qjson(db, body):
    raw=psql(db,"SELECT COALESCE(json_agg(row_to_json(x)),'[]'::json) FROM ("+body+") x;").strip()
    return json.loads(raw or "[]")

def ep(c):
    return "CASE WHEN "+c+" IS NULL OR "+c+" <= 0 THEN NULL ELSE to_timestamp("+c+") END"

def snapshot():
    return {
      "users":qjson("newapi","SELECT id,username,password AS password_hash,email,role,status,quota,used_quota,"+ep("created_at")+" AS created_at,"+ep("last_login_at")+" AS last_login_at,deleted_at AS deleted_at,COALESCE(agent_id,0) AS agent_id FROM users ORDER BY id"),
      "tokens":qjson("newapi","SELECT id,user_id,key,name,status,remain_quota,used_quota,unlimited_quota,model_limits_enabled,COALESCE(model_limits,'') AS model_limits,COALESCE(allow_ips,'') AS allow_ips,COALESCE(\"group\",'') AS group_name,"+ep("created_time")+" AS created_at,"+ep("accessed_time")+" AS last_used_at,"+ep("expired_time")+" AS expires_at,deleted_at AS deleted_at FROM tokens ORDER BY id"),
      "profiles":qjson("newapi","SELECT user_id,enabled,manual_rate_bps,current_rate_bps,total_customer_consume_quota,pending_commission_quota,transferred_quota,withdrawing_quota,withdrawn_quota,total_commission_quota,"+ep("created_at")+" AS created_at,"+ep("updated_at")+" AS updated_at FROM agent_profiles ORDER BY user_id"),
      "commissions":qjson("newapi","SELECT id,agent_user_id,customer_user_id,request_id,idempotency_key,model_name,\"group\" AS group_name,quota,commission_quota,commission_rate_bps,"+ep("created_at")+" AS created_at FROM agent_commissions ORDER BY id")
    }

def target():
    return {
      "users":qjson("sub2api","SELECT id,email,username,role,deleted_at IS NULL AS live FROM users ORDER BY id"),
      "keys":qjson("sub2api","SELECT id,key,status,deleted_at IS NULL AS live FROM api_keys ORDER BY id"),
      "groups":qjson("sub2api","SELECT id,name,deleted_at IS NULL AND status='active' AS live FROM groups ORDER BY id")
    }

def reserve(seq,n):
    if not n:return []
    return [int(x) for x in psql("sub2api","SELECT nextval('"+seq+"') FROM generate_series(1,"+str(n)+");",False).split()]

def money(v):
    return str((max(Decimal("0"),Decimal(str(v or 0)))/Q).quantize(Decimal("0.00000001")))

def norm(v): return str(v or "").strip().lower()

def arr(v):
    try:
        x=json.loads(str(v or ""))
        return json.dumps(x if isinstance(x,list) else [],ensure_ascii=False,separators=(",",":"))
    except Exception:return "[]"

def cv(v): return "\\N" if v is None else v

def agent_rate(v):
    value=int(v or 0)
    return value if value in (700,1000,1300) else 700

def require_dual_role_migration():
    applied=psql("sub2api","SELECT 1 FROM schema_migrations WHERE filename='233_allow_agent_customer_dual_role.sql';").strip()
    if applied!="1":
        raise RuntimeError("Sub migration 233_allow_agent_customer_dual_role.sql is not applied; deploy the dual-role code before migrating agent data")

def copy_part(table, cols, rows):
    out=io.StringIO(); w=csv.writer(out,lineterminator="\n")
    for row in rows:w.writerow([cv(v) for v in row])
    return "COPY "+table+" ("+",".join(cols)+") FROM STDIN WITH (FORMAT csv, NULL '\\N');\n"+out.getvalue()+"\\.\n"

def main():
    ap=argparse.ArgumentParser(); ap.add_argument("--apply",action="store_true"); a=ap.parse_args()
    s= snapshot(); t=target()
    # Fail before reserving sequence values or issuing any other write when
    # the database guard has not been upgraded to support dual-role users.
    if a.apply:
        require_dual_role_migration()
    be={}; bu={}
    for r in t["users"]:
        if norm(r["email"]):be.setdefault(norm(r["email"]),[]).append(r)
        if norm(r["username"]):bu.setdefault(norm(r["username"]),[]).append(r)
    admins=[r for r in t["users"] if r["role"]=="admin" and r["live"]]
    if len(admins)!=1: raise RuntimeError("expected one active Sub admin")
    umap={}; match={}; missing=[]; used=set()
    for r in s["users"]:
        sid=int(r["id"]); email=norm(r.get("email")); c=None; k=""
        if sid==1 and int(r.get("role") or 0)==100:c,k=admins[0],"admin"
        elif email and be.get(email):c,k=(next((x for x in be[email] if x["live"]),be[email][0]),"email")
        elif not email and bu.get(norm(r.get("username"))):c,k=bu[norm(r.get("username"))][0],"username"
        else:
            gen="newapi-user-"+str(sid)+"@migration.invalid"
            if be.get(gen):c,k=be[gen][0],"generated"
            else:missing.append(r)
        if c:
            tid=int(c["id"])
            if tid in used: raise RuntimeError("ambiguous target user mapping")
            used.add(tid); umap[sid]=tid; match[sid]=k
    ids=reserve("users_id_seq",len(missing)) if a.apply else list(range(-len(missing),0))
    for r,tid in zip(missing,ids):umap[int(r["id"])]=tid;match[int(r["id"])]="insert"
    groups={str(r["name"]):int(r["id"]) for r in t["groups"] if r["live"]}
    aliases={"default":"CCMAX20官转","plus":"gpt-plus"}
    gid=lambda n:groups.get(n) or groups.get(aliases.get(n,""))
    existing={}
    for r in t["keys"]:
        k=str(r.get("key") or ""); k=k if k.startswith("sk-") else "sk-"+k
        existing.setdefault(k,r)
    missing_keys=[r for r in s["tokens"] if (str(r.get("key") or "") if str(r.get("key") or "").startswith("sk-") else "sk-"+str(r.get("key") or "")) not in existing]
    kid=reserve("api_keys_id_seq",len(missing_keys)) if a.apply else list(range(-len(missing_keys),0))
    for r,i in zip(missing_keys,kid):
        k=str(r.get("key") or "");k=k if k.startswith("sk-") else "sk-"+k;existing[k]={"id":i}
    live_users={int(r["id"]) for r in s["users"] if int(r.get("status") or 0)==1 and r.get("deleted_at") is None}
    source_users_by_id={int(r["id"]):r for r in s["users"]}
    source_profiles_by_id={int(r["user_id"]):r for r in s["profiles"]}
    source_agent_by_user={int(r["id"]):int(r.get("agent_id") or 0) for r in s["users"]}

    # An agent is any live user referenced by a live user's direct agent_id.
    # Do not infer the role from agent_profiles: New permits a user to be both
    # an agent and a customer, and some valid agents have no profile row yet.
    relationships=[]
    skipped_relationships=0
    for r in s["users"]:
        sid=int(r["id"]); aid=int(r.get("agent_id") or 0)
        target_user=source_users_by_id.get(aid)
        if int(r.get("status") or 0)!=1 or r.get("deleted_at") is not None or aid<=0 or aid==sid:
            if aid>0 and sid in live_users: skipped_relationships+=1
            continue
        if target_user is None or aid not in live_users or sid not in umap or aid not in umap:
            skipped_relationships+=1
            continue
        relationships.append((sid,aid))
    profile_ids={aid for _,aid in relationships}
    nested=0
    summary={"source_users":len(s["users"]),"users_matched":len(s["users"])-len(missing),"users_to_insert":len(missing),"source_keys":len(s["tokens"]),"keys_to_insert":len(missing_keys),"keys_without_active_group":sum(1 for r in s["tokens"] if gid(str(r.get("group_name") or "").strip()) is None),"agent_profiles":len(profile_ids),"agent_relationships":len(relationships),"nested_agent_relations_dropped":nested,"relationships_skipped":skipped_relationships,"commissions":len(s["commissions"]),"balance_rule":"New quota / 500000 -> Sub balance; 1 RMB = 1 balance unit"}
    print(json.dumps(summary,ensure_ascii=False,indent=2))
    if not a.apply:print("DRY RUN ONLY: Sub2API was not modified.");return
    users=[]
    target_by_id={int(r["id"]):r for r in t["users"]}
    for r in s["users"]:
        sid=int(r["id"]);tid=umap[sid];old=target_by_id.get(tid);email=str(r.get("email") or "").strip()
        if sid==1 and old:email=str(old["email"])
        if not email:email="newapi-user-"+str(sid)+"@migration.invalid"
        users.append([tid,email,r.get("password_hash") or "","admin" if int(r.get("role") or 0)==100 else "user",money(r.get("quota")),"active" if int(r.get("status") or 0)==1 else "disabled",r.get("username") or "",r.get("created_at"),r.get("last_login_at"),r.get("deleted_at")])
    keys=[]
    for r in s["tokens"]:
        raw=str(r.get("key") or "");key=raw if raw.startswith("sk-") else "sk-"+raw;unsupported=bool(r.get("model_limits_enabled")) or bool(str(r.get("model_limits") or "").strip());group=gid(str(r.get("group_name") or "").strip());excluded=str(r.get("group_name") or "").strip()=="视频";status="active" if int(r.get("status") or 0)==1 and not unsupported and group is not None and not excluded else "disabled"
        total=Decimal(str(r.get("remain_quota") or 0))+Decimal(str(r.get("used_quota") or 0));quota="0" if bool(r.get("unlimited_quota")) else money(total)
        if r.get("deleted_at") is not None:status="disabled"
        allow=str(r.get("allow_ips") or "").strip()
        keys.append([int(existing[key]["id"]),umap[int(r["user_id"])],key,r.get("name") or "",group,status,r.get("created_at"),r.get("last_used_at"),r.get("deleted_at"),arr(allow) if allow else None,None,quota,money(r.get("used_quota")),r.get("expires_at")])
    profiles=[]
    for sid in sorted(profile_ids):
        r=source_profiles_by_id.get(sid)
        if r is None:
            profiles.append([umap[sid],True,0,700,"0","0","0","0","0","0",None,None])
            continue
        profiles.append([umap[sid],bool(r.get("enabled")),int(r.get("manual_rate_bps") or 0),agent_rate(r.get("current_rate_bps")),money(r.get("total_customer_consume_quota")),money(r.get("pending_commission_quota")),money(r.get("transferred_quota")),money(r.get("withdrawing_quota")),money(r.get("withdrawn_quota")),money(r.get("total_commission_quota")),r.get("created_at"),r.get("updated_at")])
    rel=[[umap[sid],umap[aid]] for sid,aid in relationships]
    comm=[]
    for r in s["commissions"]:
        au=int(r["agent_user_id"]);cu=int(r["customer_user_id"])
        if au not in profile_ids or cu not in umap or source_agent_by_user.get(cu)!=au:continue
        comm.append([umap[au],umap[cu],"newapi-migration:"+str(r["id"])+":"+str(r["idempotency_key"]),r.get("request_id") or "",r.get("model_name") or "",r.get("group_name") or "",money(r.get("quota")),money(r.get("commission_quota")),int(r.get("commission_rate_bps") or 700),r.get("created_at")])
    parts=["BEGIN;","SET LOCAL lock_timeout='5s';","SET LOCAL statement_timeout='180s';","CREATE TEMP TABLE su(id bigint,email text,password_hash text,role text,balance numeric,status text,username text,created_at timestamptz,last_login_at timestamptz,deleted_at timestamptz) ON COMMIT DROP;",copy_part("su",["id","email","password_hash","role","balance","status","username","created_at","last_login_at","deleted_at"],users),"INSERT INTO users(id,email,password_hash,role,balance,status,username,created_at,updated_at,deleted_at,last_login_at) SELECT id,email,password_hash,role,balance,status,username,COALESCE(created_at,now()),now(),deleted_at,last_login_at FROM su ON CONFLICT(id) DO UPDATE SET email=CASE WHEN users.role='admin' THEN users.email ELSE EXCLUDED.email END,password_hash=COALESCE(NULLIF(EXCLUDED.password_hash,''),users.password_hash),role=EXCLUDED.role,balance=EXCLUDED.balance,status=EXCLUDED.status,username=EXCLUDED.username,last_login_at=EXCLUDED.last_login_at,deleted_at=EXCLUDED.deleted_at,updated_at=now();","SELECT setval('users_id_seq',GREATEST((SELECT COALESCE(MAX(id),1) FROM users),(SELECT last_value FROM users_id_seq)));","CREATE TEMP TABLE sk(id bigint,user_id bigint,key text,name text,group_id bigint,status text,created_at timestamptz,last_used_at timestamptz,deleted_at timestamptz,ip_whitelist jsonb,ip_blacklist jsonb,quota numeric,quota_used numeric,expires_at timestamptz) ON COMMIT DROP;",copy_part("sk",["id","user_id","key","name","group_id","status","created_at","last_used_at","deleted_at","ip_whitelist","ip_blacklist","quota","quota_used","expires_at"],keys),"INSERT INTO api_keys(id,user_id,key,name,group_id,status,created_at,updated_at,deleted_at,ip_whitelist,ip_blacklist,quota,quota_used,expires_at) SELECT id,user_id,key,name,group_id,status,COALESCE(created_at,now()),now(),deleted_at,ip_whitelist,ip_blacklist,quota,quota_used,expires_at FROM sk ON CONFLICT(key) DO UPDATE SET user_id=EXCLUDED.user_id,name=EXCLUDED.name,group_id=EXCLUDED.group_id,status=EXCLUDED.status,updated_at=now(),deleted_at=EXCLUDED.deleted_at,ip_whitelist=COALESCE(EXCLUDED.ip_whitelist,api_keys.ip_whitelist),ip_blacklist=COALESCE(EXCLUDED.ip_blacklist,api_keys.ip_blacklist),quota=EXCLUDED.quota,quota_used=EXCLUDED.quota_used,expires_at=EXCLUDED.expires_at;","SELECT setval('api_keys_id_seq',GREATEST((SELECT COALESCE(MAX(id),1) FROM api_keys),(SELECT last_value FROM api_keys_id_seq)));","UPDATE users SET agent_id=NULL WHERE id IN (SELECT id FROM su);","CREATE TEMP TABLE sp(user_id bigint,enabled boolean,manual_rate_bps integer,current_rate_bps integer,total_customer_usage numeric,pending_commission numeric,transferred_amount numeric,withdrawing_amount numeric,withdrawn_amount numeric,total_commission numeric,created_at timestamptz,updated_at timestamptz) ON COMMIT DROP;",copy_part("sp",["user_id","enabled","manual_rate_bps","current_rate_bps","total_customer_usage","pending_commission","transferred_amount","withdrawing_amount","withdrawn_amount","total_commission","created_at","updated_at"],profiles),"INSERT INTO agent_profiles(user_id,enabled,manual_rate_bps,current_rate_bps,total_customer_usage,pending_commission,transferred_amount,withdrawing_amount,withdrawn_amount,total_commission,created_at,updated_at) SELECT user_id,enabled,manual_rate_bps,current_rate_bps,total_customer_usage,pending_commission,transferred_amount,withdrawing_amount,withdrawn_amount, total_commission,COALESCE(created_at,now()),COALESCE(updated_at,now()) FROM sp ON CONFLICT(user_id) DO UPDATE SET enabled=EXCLUDED.enabled,manual_rate_bps=EXCLUDED.manual_rate_bps,current_rate_bps=EXCLUDED.current_rate_bps,total_customer_usage=EXCLUDED.total_customer_usage,pending_commission=EXCLUDED.pending_commission,transferred_amount=EXCLUDED.transferred_amount,withdrawing_amount=EXCLUDED.withdrawing_amount,withdrawn_amount=EXCLUDED.withdrawn_amount,total_commission=EXCLUDED.total_commission,updated_at=EXCLUDED.updated_at;","CREATE TEMP TABLE sr(user_id bigint,agent_id bigint) ON COMMIT DROP;",copy_part("sr",["user_id","agent_id"],rel),"UPDATE users u SET agent_id=sr.agent_id FROM sr WHERE u.id=sr.user_id;","CREATE TEMP TABLE sc(agent_user_id bigint,customer_user_id bigint,idempotency_key text,request_id text,model_name text,group_name text,usage_amount numeric,commission_amount numeric,commission_rate_bps integer,created_at timestamptz) ON COMMIT DROP;",copy_part("sc",["agent_user_id","customer_user_id","idempotency_key","request_id","model_name","group_name","usage_amount","commission_amount","commission_rate_bps","created_at"],comm),"INSERT INTO agent_commissions(agent_user_id,customer_user_id,idempotency_key,request_id,model_name,group_name,usage_amount,commission_amount,commission_rate_bps,created_at) SELECT agent_user_id,customer_user_id,idempotency_key,request_id,model_name,group_name,usage_amount,commission_amount,commission_rate_bps,COALESCE(created_at,now()) FROM sc ON CONFLICT(idempotency_key) DO UPDATE SET agent_user_id=EXCLUDED.agent_user_id,customer_user_id=EXCLUDED.customer_user_id,request_id=EXCLUDED.request_id,model_name=EXCLUDED.model_name,group_name=EXCLUDED.group_name,usage_amount=EXCLUDED.usage_amount,commission_amount=EXCLUDED.commission_amount,commission_rate_bps=EXCLUDED.commission_rate_bps,created_at=EXCLUDED.created_at;","SELECT setval('agent_commissions_id_seq',GREATEST((SELECT COALESCE(MAX(id),1) FROM agent_commissions),(SELECT last_value FROM agent_commissions_id_seq)));","COMMIT;"]
    psql("sub2api","-- stdin migration",False,"\n".join(parts).encode());print("APPLY COMMITTED")
if __name__=="__main__":main()
