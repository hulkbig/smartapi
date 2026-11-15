import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import {HttpSmartAPIAdapter} from './api.js';

const adapter=new HttpSmartAPIAdapter();
const base={id:'speed',status:'success',duration_ms:2500,ttft_ms:500,usage:{input_tokens:1000,output_tokens:125,reasoning_tokens:25}};
// Request throughput includes waiting/recovery; reasoning tokens are not added twice.
for(const stream of [true,false])assert.equal(adapter.mapRequest({...base,stream}).tokensPerSecond,50);
assert.equal(adapter.mapRequest({...base,usage:{input_tokens:100,output_tokens:0}}).tokensPerSecond,0);
for(const patch of [
  {status:'streaming'},{status:'waiting'},{usage:null},{usage:{input_tokens:100}},
  {usage:{input_tokens:100,output_tokens:null}},{usage:{input_tokens:100,output_tokens:-1}},
  {usage:{input_tokens:100,output_tokens:Infinity}},
  ...[0,-1,null,undefined,NaN,Infinity,'2500'].map(duration_ms=>({duration_ms})),
])assert.equal(adapter.mapRequest({...base,...patch}).tokensPerSecond,null);

const source=await fs.readFile(new URL('./app.part11.jsfrag',import.meta.url),'utf8');
const context=vm.createContext({Number,Math,detailFailure:()=>'',esc:String,nt:en=>en,requestPricingLabel:()=>'',requestMoney:String,
  state:{requestCache:new Map()},queueMicrotask(){},badge:String,requestCostDetails:()=>'',fmtCompact:String,icon:()=>'',uiHint:()=>''});
for(const name of ['capabilityNames','requestTimingTone','requestAccountingCells','requestKeySubtitle','requestTimingSegments','requestTimingWaterfall','requestAttemptTable','pageRequestDetail']){
  const start=source.indexOf('function '+name+'('),end=source.indexOf('\nfunction ',start+1);
  vm.runInContext(source.slice(start,end<0?undefined:end),context);
}
for(const [request,label] of [[base,'50.0'],[{...base,status:'streaming'},'—'],[{...base,usage:null},'—']]){
  const row=adapter.mapRequest(request);
  context.requestDataSync=()=>row;
  assert.ok(context.requestAccountingCells(row).includes(`${label} tokens/s`));
  assert.ok(context.pageRequestDetail('speed').includes(`<strong>${label}</strong><small>tokens/s</small>`));
}
console.log('Request tokens/s mapping, list and details: PASS');

const modelSource=await fs.readFile(new URL('./app.part3.jsfrag',import.meta.url),'utf8');
context.state={models:[],modelsSearch:'',modelStatus:'',modelType:'',overviewWindow:'15m'};
context.scenarioView=()=>({});context.scenarioBanner=()=>'';context.demoMode=false;context.resourceHash=(kind,id)=>`#/${kind}/${id}`;context.translatedConsoleText=String;
for(const name of ['modelIsEnabled','modelLifecycle','modelSpeedMetric','modelSupplySummary','pageModels']){
 const start=modelSource.indexOf('function '+name+'('),end=modelSource.indexOf('\nfunction ',start+1);vm.runInContext(modelSource.slice(start,end<0?undefined:end),context);
}
const model=adapter.mapModel({id:'logical',enabled:true,traffic:{output_tokens_per_second:42.25,speed_samples:9},default_route:[{provider_id:'p1',upstream_model:'same-name',output_tokens_per_second:80,speed_samples:6},{provider_id:'p2',upstream_model:'same-name',output_tokens_per_second:12.5,speed_samples:3}]},1440,new Map([['p1','First provider'],['p2','Fallback provider']]));
assert.equal(model.tokensPerSecond,42.25);assert.equal(model.routes[1].tokensPerSecond,12.5);assert.equal(model.routes[0].speedWindowMinutes,1440);
context.state.models=[model];const list=context.pageModels();
for(const value of ['42.3','80.0','12.5','First provider','Fallback provider','token/s'])assert.ok(list.includes(value),value+' must be visible directly in list');
assert.match(list,/Samples: 9/);assert.match(list,/Period: 1 days/);
assert.equal(adapter.mapModel({id:'no-data'},15,new Map()).tokensPerSecond,null);
assert.match(context.modelSpeedMetric(null,0),/—/);assert.match(context.modelSpeedMetric(0,1),/>0.0</);
assert.doesNotMatch(context.modelSpeedMetric(NaN,1),/NaN/);
console.log('PASS logical and all actual upstream Token/s visible in list, provider isolation, sample/period hints and unknown versus zero');
