import assert from 'node:assert/strict';
import {buildRouteViewModel,observationQuery,previewOf,previewPlaceholder,focusedSource} from './observability-model.js';
import {HttpSmartAPIAdapter,SmartAPIError} from './api.js';

const candidates=[
  {candidate_id:'a',route_step:1,provider_id:'p1',provider_name:'Historical One',connection_id:'c1',eligible:true},
  {candidate_id:'b',route_step:1,provider_id:'p1',provider_name:'Historical One',connection_id:'c2',eligible:true},
  {candidate_id:'c',route_step:2,provider_id:'p2',provider_name:'Removed Two',connection_id:'c3',eligible:false,exclusion_reason:'quota exhausted'},
];
const events=[
  {seq:1,type:'REQUEST_RECEIVED'},
  {seq:2,type:'CONNECTION_SELECTED',provider_id:'p1',connection_id:'c1',route_step:1},
  {seq:3,type:'ATTEMPT_STARTED',provider_id:'p1',connection_id:'c1',route_step:1},
  {seq:4,type:'UPSTREAM_DISPATCHED',provider_id:'p1',connection_id:'c1',route_step:1},
  {seq:5,type:'ATTEMPT_FAILED',provider_id:'p1',connection_id:'c1',route_step:1,failure_type:'rate_limit'},
  {seq:6,type:'CONNECTION_REBOUND',provider_id:'p1',connection_id:'c2',route_step:1},
  {seq:7,type:'ATTEMPT_STARTED',provider_id:'p1',connection_id:'c2',route_step:1},
  {seq:8,type:'UPSTREAM_DISPATCHED',provider_id:'p1',connection_id:'c2',route_step:1},
  {seq:9,type:'CLIENT_FIRST_OUTPUT'},
  {seq:10,type:'CLIENT_DELIVERY_COMPLETED'},
];
const detail={client_request:{id:'r',session_id:'s',status:'success'},route_snapshot:{coverage:'complete',candidates,route_steps:[
  {route_step:1,provider_id:'p1',provider_name:'Historical One'},
  {route_step:2,provider_id:'p2',provider_name:'Removed Two'},
  {route_step:3,provider_id:'p3',provider_name:'No connections',state:'no_connections'},
]},execution_trace_events:events};
let vm=buildRouteViewModel(detail,3);
assert.equal(vm.phase,'waiting');assert.equal(vm.packet,null);assert.equal(vm.dispatched,false);
assert.equal(vm.providers.flatMap(p=>p.candidates).some(c=>c.active),false,'selected while waiting is not an active upstream call');
assert.equal(vm.providers.length,3);assert.equal(vm.candidateCount,3,'excluded and empty route steps remain accessible');
vm=buildRouteViewModel(detail,5);
assert.equal(vm.packet,'upstream-error','recoverable errors return to gateway, not the client');
assert.equal(vm.providers[0].candidates[0].failed,true);
const admission=buildRouteViewModel({...detail,execution_trace_events:[...events.slice(0,3),{seq:4,type:'ATTEMPT_FAILED',provider_id:'p1',connection_id:'c1',route_step:1}]});
assert.equal(admission.phase,'admission-failed');assert.equal(admission.packet,null,'failed admission must not animate an upstream response');
vm=buildRouteViewModel(detail,8);
assert.equal(vm.providers.flatMap(p=>p.candidates).filter(c=>c.active).length,1);
assert.equal(vm.providers[0].candidates[1].active,true,'same Provider retry selects the actual connection');
assert.equal(vm.providers[1].provider_name,'Removed Two','snapshot names survive deletion');
vm=buildRouteViewModel(detail,9);assert.equal(vm.packet,'client-response');
vm=buildRouteViewModel({...detail,client_request:{...detail.client_request,status:'canceled'},execution_trace_events:[...events,{seq:11,type:'CLIENT_DELIVERY_INTERRUPTED'}]});
assert.equal(vm.phase,'canceled');assert.equal(vm.packet,null);
assert.equal(buildRouteViewModel({}).coverage,'legacy');
assert.equal(previewOf({}).content_state,'legacy_unavailable');
assert.equal(previewPlaceholder({content_state:'disabled'},'zh'),'内容未记录');
assert.ok(!observationQuery({from:'start',to:'end',window:'24h'}).includes('window='),'explicit bounds exclude window');
assert.equal(observationQuery({scope:'all',window:'24h'}),'scope=all');
const focused=focusedSource({source_id:'session:s',request_count:7,selected_request_status:'waiting'}, {...detail,client_request:{...detail.client_request,content_preview:{preview_role:'response',preview_text:'visible',content_state:'recorded'}}});
assert.equal(focused.selected_request_id,'r');assert.equal(focused.selected_request_status,'success');assert.equal(focused.request_count,7);assert.equal(focused.content_preview.preview_text,'visible');

const calls=[];
const api=new HttpSmartAPIAdapter('',async(path,options={})=>{
  calls.push({path,options});
  if(path.startsWith('/api/requests/r'))return Response.json({...detail,client_request:{...detail.client_request,usage:{input_tokens:0,output_tokens:0},usage_state:'known',cost_state:'known',total_cost:0}});
  return Response.json({total:0,sessions:[],sources:[],requests:[]});
});
const controller=new AbortController();
await api.getSessions({from:'start',to:'end',window:'24h'},{signal:controller.signal});
assert.equal(calls[0].options.signal,controller.signal);
assert.ok(!calls[0].path.includes('window='));
await api.getProviderTraffic('provider/with space',{mode:'request'});
assert.match(calls[1].path,/providers\/provider%2Fwith%20space\/traffic/);
const mapped=await api.getRequest('r');
assert.equal(mapped.sessionId,'s');assert.equal(mapped.routeSnapshot.candidates.length,3);assert.equal(mapped.executionEvents.length,10);
assert.equal(mapped.usageObserved,true,'explicit zero usage must remain observed');
const broken=new HttpSmartAPIAdapter('',async()=>Response.json({error:{message:'History offline'}},{status:503}));
await assert.rejects(()=>broken.getSessions(),error=>error instanceof SmartAPIError&&error.status===503,'real errors must not fall back to Mock data');
console.log('Session observability contracts: PASS');
