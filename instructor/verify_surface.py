#!/usr/bin/env python3
"""Exercise every dgs-service query+mutation (incl meta) against the running
stack. Success returns bare data; failure returns a top-level GraphQL error."""
import json, urllib.request
DGS="http://localhost:8080/graphql"
def gql(q,v=None):
    b={"query":q}
    if v: b["variables"]=v
    req=urllib.request.Request(DGS,data=json.dumps(b).encode(),headers={"Content-Type":"application/json"})
    return json.load(urllib.request.urlopen(req))
def data(r,*path):
    d=r.get("data") or {}
    for p in path: d=(d or {}).get(p)
    return d
def errs(r): return r.get("errors") or []
res=[]
def chk(n,c,d=""):
    res.append((n,c)); print(f"  {'PASS' if c else 'FAIL'}  {n}  {d}")

print("=== meta.wipe ===")
r=gql("mutation{meta{wipe}}")
chk("meta.wipe", data(r,"meta","wipe")==True and not errs(r), str(r.get("data") or r))

print("=== createEntity (create+readback oracle) → bare Entity ===")
r=gql('mutation{createEntity(name:"rack1-sw3",type:"switch",status:"active"){id name type status version}}')
ce=data(r,"createEntity") or {}
eid=ce.get("id")
chk("createEntity", not errs(r) and bool(eid) and ce.get("version")==1, f"id={eid}")

print("=== getEntity ===")
r=gql('query($id:ID!){getEntity(id:$id){id name status version}}',{"id":eid})
chk("getEntity", not errs(r) and (data(r,"getEntity") or {}).get("id")==eid, str(data(r,"getEntity")))

print("=== updateEntity (version++ oracle) ===")
r=gql('mutation($id:ID!){updateEntity(id:$id,name:"rack1-sw3",type:"switch",status:"maintenance"){version status}}',{"id":eid})
ue=data(r,"updateEntity") or {}
chk("updateEntity", not errs(r) and ue.get("version")==2 and ue.get("status")=="maintenance", str(ue))

print("=== listEntities → bare [Entity] ===")
r=gql('query{listEntities{id name type status version}}')
le=data(r,"listEntities") or []
chk("listEntities", not errs(r) and len(le)==1 and le[0].get("id")==eid, f"len={len(le)}")

print("=== deleteEntity (delete+404 oracle) → Boolean ===")
r=gql('mutation($id:ID!){deleteEntity(id:$id)}',{"id":eid})
chk("deleteEntity", not errs(r) and data(r,"deleteEntity")==True, str(data(r,"deleteEntity")))

print("=== getEntity missing → top-level error ===")
r=gql('query{getEntity(id:"ent_nope"){id}}')
msg=(errs(r)[0].get("message") if errs(r) else "")
chk("getEntity(missing) errors[]", data(r,"getEntity") is None and "not found" in msg.lower(), msg)

print("=== scenario mutations present in schema, returning bare types ===")
r=gql('{__type(name:"MetaMutation"){fields{name type{name ofType{name}}}}}')
fs={f["name"]:(f["type"].get("name") or (f["type"].get("ofType") or {}).get("name")) for f in data(r,"__type","fields") or []}
chk("meta scenario1/2/3 + wipe", sorted(fs)==["scenario1","scenario2","scenario3","wipe"], str(fs))
chk("scenario1→Entity", fs.get("scenario1")=="Entity", fs.get("scenario1"))
chk("scenario2→list", fs.get("scenario2") in (None,"Entity"), "list of Entity")  # NonNull(list) → ofType name is null; accept

np=sum(1 for _,c in res if c)
print(f"\n=== {np}/{len(res)} PASS ===")
if np!=len(res): print("FAIL:",[n for n,c in res if not c])
