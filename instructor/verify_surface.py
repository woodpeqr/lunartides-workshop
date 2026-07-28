#!/usr/bin/env python3
"""Exercise every dgs-service query+mutation (incl meta) against the running stack."""
import json, urllib.request
DGS="http://localhost:8080/graphql"
def gql(q,v=None):
    b={"query":q}
    if v: b["variables"]=v
    req=urllib.request.Request(DGS,data=json.dumps(b).encode(),headers={"Content-Type":"application/json"})
    return json.load(urllib.request.urlopen(req))
res=[]
def chk(n,c,d=""):
    res.append((n,c)); print(f"  {'PASS' if c else 'FAIL'}  {n}  {d}")

print("=== meta.wipe (clean) ===")
r=gql("mutation{meta{wipe{verdict message}}}")
chk("meta.wipe", r.get("data",{}).get("meta",{}).get("wipe",{}).get("verdict")=="PASS", str(r.get("data") or r))

print("=== createEntity (create+readback oracle) ===")
r=gql('mutation{createEntity(name:"rack1-sw3",type:"switch",status:"active"){verdict message entity{id name type status version}}}')
ce=r.get("data",{}).get("createEntity",{})
eid=(ce.get("entity") or {}).get("id")
chk("createEntity", ce.get("verdict")=="PASS" and bool(eid), f"id={eid} v={ (ce.get('entity') or {}).get('version')}")

print("=== getEntity ===")
r=gql('query($id:ID!){getEntity(id:$id){verdict message entity{id name status version}}}',{"id":eid})
ge=r.get("data",{}).get("getEntity",{})
chk("getEntity", ge.get("verdict")=="PASS" and (ge.get("entity") or {}).get("id")==eid, str(ge.get("entity")))

print("=== updateEntity (version++ oracle) ===")
r=gql('mutation($id:ID!){updateEntity(id:$id,name:"rack1-sw3",type:"switch",status:"maintenance"){verdict message entity{version status}}}',{"id":eid})
ue=r.get("data",{}).get("updateEntity",{})
chk("updateEntity", ue.get("verdict")=="PASS" and (ue.get("entity") or {}).get("version")==2, f"v={(ue.get('entity') or {}).get('version')} status={(ue.get('entity') or {}).get('status')}")

print("=== listEntities ===")
r=gql('query{listEntities{verdict message count}}')
le=r.get("data",{}).get("listEntities",{})
chk("listEntities", le.get("verdict")=="PASS" and le.get("count")==1, f"count={le.get('count')}")

print("=== probeRoundTrip (primary oracle) ===")
r=gql('mutation{probeRoundTrip(name:"router-a",type:"router",status:"active"){verdict message entity{id version}}}')
pr=r.get("data",{}).get("probeRoundTrip",{})
chk("probeRoundTrip", pr.get("verdict")=="PASS", str(pr.get("message")))

print("=== deleteEntity (delete+404 oracle) ===")
r=gql('mutation($id:ID!){deleteEntity(id:$id){verdict message}}',{"id":eid})
de=r.get("data",{}).get("deleteEntity",{})
chk("deleteEntity", de.get("verdict")=="PASS", str(de.get("message")))

print("=== getEntity missing (oracle FAIL expected) ===")
r=gql('query{getEntity(id:"ent_nope"){verdict message}}')
gm=r.get("data",{}).get("getEntity",{})
chk("getEntity(missing)=FAIL", gm.get("verdict")=="FAIL" and "not found" in gm.get("message","").lower(), str(gm.get("message")))

print("=== meta.scenarioStatus inactive ===")
r=gql("query{meta{scenarioStatus{id active since}}}")
ss=r.get("data",{}).get("meta",{}).get("scenarioStatus",{})
chk("scenarioStatus inactive", ss.get("active")==False, str(ss))

print("=== meta.scenario(99) unknown ===")
r=gql("mutation{meta{scenario(id:99){active message}}}")
s99=r.get("data",{}).get("meta",{}).get("scenario",{})
chk("scenario(99) rejected", s99.get("active")==False and "unknown" in s99.get("message","").lower(), str(s99))

np=sum(1 for _,c in res if c)
print(f"\n=== {np}/{len(res)} PASS ===")
if np!=len(res): print("FAIL:",[n for n,c in res if not c])
