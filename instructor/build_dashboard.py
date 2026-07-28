#!/usr/bin/env python3
"""Build the entity-service observability dashboard + push into grafana.db.
Overwrites the existing dashboard uid. Metric names verified against Prometheus."""
import json, base64, urllib.request

GRAFANA="http://localhost:3001"; AUTH="admin:lunartides"; UID="efth31on99ptsc"
PROM={"type":"prometheus","uid":"prometheus"}; LOKI={"type":"loki","uid":"loki"}
_id=[0]
def nid(): _id[0]+=1; return _id[0]
def gp(x,y,w,h): return {"h":h,"w":w,"x":x,"y":y}
def ts(title,exprs,g,unit="short",desc=""):
    t=[{"datasource":PROM,"expr":e,"legendFormat":l,"refId":chr(65+i),"range":True} for i,(e,l) in enumerate(exprs)]
    return {"id":nid(),"type":"timeseries","title":title,"description":desc,"datasource":PROM,"gridPos":g,
        "targets":t,"fieldConfig":{"defaults":{"unit":unit,"custom":{"drawStyle":"line","fillOpacity":10,"showPoints":"never","lineWidth":2}},"overrides":[]},
        "options":{"legend":{"displayMode":"table","placement":"bottom","calcs":["last","max"]},"tooltip":{"mode":"multi"}}}
def stat(title,expr,g,unit="short",th=None,desc="",leg=""):
    steps=th or [{"color":"green","value":None}]
    return {"id":nid(),"type":"stat","title":title,"description":desc,"datasource":PROM,"gridPos":g,
        "targets":[{"datasource":PROM,"expr":expr,"legendFormat":leg,"refId":"A","range":True}],
        "fieldConfig":{"defaults":{"unit":unit,"thresholds":{"mode":"absolute","steps":steps},"color":{"mode":"thresholds"}},"overrides":[]},
        "options":{"graphMode":"area","colorMode":"value","reduceOptions":{"calcs":["lastNotNull"]}}}
def logs(title,expr,g):
    return {"id":nid(),"type":"logs","title":title,"datasource":LOKI,"gridPos":g,
        "targets":[{"datasource":LOKI,"expr":expr,"refId":"A","queryType":"range"}],
        "options":{"showTime":True,"wrapLogMessage":True,"sortOrder":"Descending","enableLogDetails":True}}
def row(title,y): return {"id":nid(),"type":"row","title":title,"gridPos":gp(0,y,24,1),"collapsed":False}

P=[]
# RED
P.append(row("Service health — RED",0))
P.append(stat("Request rate","sum(rate(lunartides_entity_requests_total[1m]))",gp(0,1,4,4),"reqps",desc="Total req/s."))
P.append(stat("Error rate","sum(rate(lunartides_entity_requests_errors_total[1m]))",gp(4,1,4,4),"reqps",
    [{"color":"green","value":None},{"color":"red","value":0.001}],desc="4xx+5xx req/s. Corruption scenario spikes this."))
P.append(stat("In-flight","sum(lunartides_entity_requests_in_flight)",gp(8,1,4,4),"short",
    [{"color":"green","value":None},{"color":"yellow","value":20},{"color":"red","value":50}],desc="Concurrent requests."))
P.append(stat("p95 latency","histogram_quantile(0.95, sum by (le) (rate(lunartides_entity_request_duration_milliseconds_bucket[5m])))",gp(12,1,6,4),"ms",
    [{"color":"green","value":None},{"color":"yellow","value":250},{"color":"red","value":500}],desc="Slow-list scenario ramps this."))
P.append(stat("Go memory used","sum(lunartides_go_memory_used_bytes)",gp(18,1,6,4),"bytes",
    [{"color":"green","value":None},{"color":"yellow","value":150000000},{"color":"red","value":200000000}],desc="OOM signal — flood ramps this to the 256m limit."))
# Traffic & errors
P.append(row("Traffic & errors",5))
P.append(ts("Request rate by operation",[("sum by (entity_operation) (rate(lunartides_entity_requests_total[1m]))","{{entity_operation}}")],gp(0,6,12,8),"reqps"))
P.append(ts("Error rate by operation & status",[("sum by (entity_operation, entity_response_status) (rate(lunartides_entity_requests_errors_total[1m]))","{{entity_operation}} {{entity_response_status}}")],gp(12,6,12,8),"reqps",
    desc="Corruption scenario: 5xx climb here as the store file tears."))
# Latency (traces lesson)
P.append(row("Latency — the slow-list (traces) signal",14))
P.append(ts("Request duration percentiles",[
    ("histogram_quantile(0.50, sum by (le) (rate(lunartides_entity_request_duration_milliseconds_bucket[5m])))","p50"),
    ("histogram_quantile(0.95, sum by (le) (rate(lunartides_entity_request_duration_milliseconds_bucket[5m])))","p95"),
    ("histogram_quantile(0.99, sum by (le) (rate(lunartides_entity_request_duration_milliseconds_bucket[5m])))","p99")],gp(0,15,12,8),"ms"))
P.append(ts("Store op p95 latency by op",[("histogram_quantile(0.95, sum by (le, entity_store_op) (rate(lunartides_entity_store_operation_duration_milliseconds_bucket[5m])))","{{entity_store_op}}")],gp(12,15,12,8),"ms",
    desc="List/Load ops dominate as the store file grows — the trace waterfall confirms the whole-file scan is the hot path."))
# Store state (metrics lesson)
P.append(row("Store state — resource growth (metrics)",23))
P.append(ts("Go memory used (OOM ramp)",[("sum(lunartides_go_memory_used_bytes)","total"),("sum by (go_memory_type) (lunartides_go_memory_used_bytes)","{{go_memory_type}}")],gp(0,24,12,8),"bytes",
    desc="Flood ramps heap until the container hits mem_limit (256m)."))
P.append(ts("Store file size + entity count",[("lunartides_entity_store_file_bytes","file bytes"),("lunartides_entity_store_count","entity count")],gp(12,24,12,8),"bytes",
    desc="The JSON store file grows unbounded — every op re-marshals all of it."))
P.append(ts("Store operation errors",[("sum by (entity_store_op) (rate(lunartides_entity_store_operation_errors_total[1m]))","{{entity_store_op}}"),("rate(lunartides_entity_store_parse_errors_total[1m])","parse errors")],gp(0,32,12,8),"ops",
    desc="Parse errors = the store file was torn by concurrent writes (corruption scenario)."))
# Logs lesson
P.append(row("Logs — the corruption signal",40))
P.append(logs("entity-service warn & error logs (trace-correlated)",'{service_name="entity-service"} | detected_level=~"warn|error"',gp(0,41,24,10)))
P.append(logs("Store-file parse failures (root cause of corruption 500s)",'{service_name="entity-service"} |= "failed to parse entity store file"',gp(0,51,24,8)))

dash={"uid":UID,"title":"entity-service — Observability","tags":["entity-service","workshop"],
    "timezone":"browser","schemaVersion":39,"refresh":"5s","time":{"from":"now-15m","to":"now"},
    "templating":{"list":[]},"panels":P}
body=json.dumps({"dashboard":dash,"overwrite":True,"message":"entity-service observability dashboard"}).encode()
req=urllib.request.Request(f"{GRAFANA}/api/dashboards/db",data=body,headers={"Content-Type":"application/json"})
req.add_header("Authorization","Basic "+base64.b64encode(AUTH.encode()).decode())
try:
    r=urllib.request.urlopen(req); print("DASHBOARD:",r.status,r.read().decode()[:200])
except urllib.error.HTTPError as e: print("ERR:",e.code,e.read().decode()[:300])
