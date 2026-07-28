#!/usr/bin/env python3
"""Create the 3 entity-service alert rules (one per scenario) via provisioning API."""
import json, base64, urllib.request, urllib.error
GRAFANA="http://localhost:3001"; AUTH="Basic "+base64.b64encode(b"admin:lunartides").decode()
FOLDER_UID="entity-alerts"; DS="prometheus"
def call(method,path,body=None,hdr=None):
    data=json.dumps(body).encode() if body is not None else None
    req=urllib.request.Request(GRAFANA+path,data=data,method=method)
    req.add_header("Authorization",AUTH); req.add_header("Content-Type","application/json")
    for k,v in (hdr or {}).items(): req.add_header(k,v)
    try:
        r=urllib.request.urlopen(req); return r.status,r.read().decode()
    except urllib.error.HTTPError as e: return e.code,e.read().decode()

# folder
st,out=call("POST","/api/folders",{"uid":FOLDER_UID,"title":"entity-service alerts"})
print("FOLDER:",st,out[:120])
# delete any pre-existing (idempotent re-run)
for uid in ["es-flood-oom","es-slow-list","es-corruption"]:
    call("DELETE","/api/v1/provisioning/alert-rules/"+uid)

def pq(expr): return {"refId":"A","queryType":"","relativeTimeRange":{"from":600,"to":0},"datasourceUid":DS,
    "model":{"expr":expr,"instant":True,"refId":"A","datasource":{"type":"prometheus","uid":DS}}}
def thr(op,val): return [
    {"refId":"B","queryType":"","datasourceUid":"__expr__","relativeTimeRange":{"from":600,"to":0},
     "model":{"type":"reduce","reducer":"last","expression":"A","refId":"B","datasource":{"type":"__expr__","uid":"__expr__"}}},
    {"refId":"C","queryType":"","datasourceUid":"__expr__","relativeTimeRange":{"from":600,"to":0},
     "model":{"type":"threshold","expression":"B","refId":"C","datasource":{"type":"__expr__","uid":"__expr__"},
       "conditions":[{"type":"query","evaluator":{"type":op,"params":[val]}}]}}]
def rule(uid,title,expr,op,val,dur,summary,teaches):
    return {"uid":uid,"title":title,"condition":"C","folderUID":FOLDER_UID,"ruleGroup":"entity-service",
        "noDataState":"OK","execErrState":"Error","for":dur,"orgID":1,
        "data":[pq(expr)]+thr(op,val),
        "annotations":{"summary":summary,"teaches":teaches},"labels":{"service":"entity-service"}}

rules=[
    rule("es-flood-oom","entity-service memory approaching limit (flood)",
         "sum(lunartides_go_memory_used_bytes)","gt",130_000_000,"20s",
         "Go memory > 130MB (container mem_limit 256m). Create-flood is ramping the heap toward OOM.","metrics (scenario 1)"),
    rule("es-slow-list","entity-service list latency high (slow-list)",
         "histogram_quantile(0.95, sum by (le) (rate(lunartides_entity_request_duration_milliseconds_bucket{entity_operation=\"entity.list\"}[5m])))","gt",30,"20s",
         "GET /entities p95 > 30ms (healthy is ~1ms). It re-scans the whole store file; latency climbs with entity count. The trace shows the list/load span dominating.","traces (scenario 2)"),
    rule("es-corruption","entity-service error rate high (corruption)",
         "sum(rate(lunartides_entity_requests_errors_total[1m]))","gt",0.2,"20s",
         "5xx error rate elevated. Concurrent non-atomic writes tore the store file; reads now fail to parse. The error LOG names the cause.","logs (scenario 3)"),
]
for r in rules:
    st,out=call("POST","/api/v1/provisioning/alert-rules",r,{"X-Disable-Provenance":"true"})
    print(f"RULE {r['uid']}:",st,out[:100])

# Evaluate every 10s so a fast ramp is caught (default 60s is too coarse for
# scenarios that cross threshold and then OOM within a minute).
st,out=call("PUT","/api/v1/provisioning/folder/"+FOLDER_UID+"/rule-groups/entity-service",
    {"title":"entity-service","interval":10,"rules":rules},{"X-Disable-Provenance":"true"})
print("GROUP interval->10s:",st,out[:80])
