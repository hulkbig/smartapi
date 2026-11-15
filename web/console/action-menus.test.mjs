import assert from 'node:assert/strict';
import {installActionMenus} from './ui-controls.js';
const listeners=new Map(),windowListeners=new Map();
const doc={addEventListener:(name,fn)=>{if(!listeners.has(name))listeners.set(name,[]);listeners.get(name).push(fn)}};
const win={innerWidth:390,innerHeight:700,addEventListener:(name,fn)=>windowListeners.set(name,fn)};
const emit=(name,target,extra={})=>{const event={target,preventDefault(){this.prevented=true},stopImmediatePropagation(){this.stopped=true},...extra};for(const fn of listeners.get(name)||[])fn(event);return event};
function fixture(native=true){
  const trigger={isConnected:true,attrs:{},setAttribute(k,v){this.attrs[k]=v},getBoundingClientRect:()=>({right:382,top:640,bottom:684}),focus(){this.focused=true},closest:()=>null};
  const panel={style:{},shown:false,attrs:{},matches:()=>panel.shown,setAttribute(k,v){this.attrs[k]=v},getBoundingClientRect:()=>({width:190,height:150}),contains:node=>node===panel};
  if(native){panel.showPopover=()=>{panel.shown=true};panel.hidePopover=()=>{panel.shown=false};}
  const action={closest:selector=>selector==='button,a[href]'?action:null};
  const menu={open:true,isConnected:true,matches:selector=>selector==='.v3-more-menu',querySelector:selector=>selector==='summary'?trigger:panel,contains:node=>[menu,panel,trigger,action].includes(node)};
  panel.parentNode=menu;
  return {menu,panel,trigger,action};
}
installActionMenus(doc,win);
const first=fixture();emit('toggle',first.menu);
assert.equal(first.panel.parentNode,first.menu,'menu actions keep their delegated event ancestry');
assert.equal(first.panel.shown,true);assert.equal(first.trigger.attrs['aria-expanded'],'true');
assert.ok(parseFloat(first.panel.style.left)>=8&&parseFloat(first.panel.style.left)+190<=382,'popup fits narrow viewport');
assert.ok(parseFloat(first.panel.style.top)+150<640,'popup flips above a trigger near the bottom');
const escape=emit('keydown',first.trigger,{key:'Escape'});
assert.equal(first.menu.open,false);assert.equal(first.panel.shown,false);assert.equal(first.trigger.focused,true);assert.equal(escape.prevented,true);
first.menu.open=true;emit('toggle',first.menu);
const second=fixture();emit('toggle',second.menu);assert.equal(first.menu.open,false,'opening another menu closes the first');
const click=emit('click',second.action);assert.equal(second.menu.open,false);assert.equal(click.prevented,undefined);assert.equal(click.stopped,undefined,'menu action clicks propagate normally');
const fallback=fixture(false);emit('toggle',fallback.menu);assert.ok(fallback.panel.style.left);emit('scroll',fallback.panel);assert.equal(fallback.menu.open,true);
emit('scroll',doc);assert.equal(fallback.menu.open,false,'page/table scrolling dismisses a stale anchor');
fallback.menu.open=true;emit('toggle',fallback.menu);emit('click',{closest:()=>null});assert.equal(fallback.menu.open,false);
console.log('PASS action menu clipping bounds, upward placement, keyboard/outside dismissal and unchanged action propagation');
