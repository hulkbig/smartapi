import assert from 'node:assert/strict';
import {installTooltips} from './ui-controls.js';
const listeners=new Map(),windowListeners=new Map();
class Element{
 constructor(){this.attributes=new Map();this.dataset={};this.style={};this.isConnected=true;this.hover=false;this.rect={left:375,top:100,bottom:124,width:24,height:24}}
 setAttribute(k,v){this.attributes.set(k,String(v))}getAttribute(k){return this.attributes.get(k)??null}removeAttribute(k){this.attributes.delete(k)}hasAttribute(k){return this.attributes.has(k)}
 getBoundingClientRect(){return this.rect}matches(selector){return selector===':hover'&&this.hover}closest(selector){return selector==='[data-tooltip]'&&this.dataset.tooltip?this:null}
}
const doc={activeElement:null,createElement:()=>{const el=new Element();el.rect={left:0,top:0,width:300,height:56};return el},body:{append(){}},addEventListener:(type,fn)=>{if(!listeners.has(type))listeners.set(type,[]);listeners.get(type).push(fn)}};
const win={innerWidth:390,innerHeight:700,addEventListener:(type,fn)=>windowListeners.set(type,fn)};
const tip=installTooltips(doc,win,value=>'译 '+value);
const fire=(type,target,extra={})=>{const event={target,preventDefault(){this.prevented=true},stopImmediatePropagation(){this.stopped=true},...extra};for(const fn of listeners.get(type)||[])fn(event);return event};
const help=new Element();help.dataset.tooltip='Quota definition';help.setAttribute('data-help','');help.setAttribute('aria-describedby','existing-help');
fire('pointerover',help);assert.equal(tip.element.hidden,false);assert.equal(tip.element.textContent,'译 Quota definition');assert.equal(help.getAttribute('aria-describedby'),'existing-help console-tooltip');
assert.ok(parseInt(tip.element.style.left)+300<=382,'tooltip must fit the narrow viewport');
doc.activeElement=help;fire('focusin',help);const escape=fire('keydown',help,{key:'Escape'});assert.equal(escape.stopped,true);assert.equal(tip.element.hidden,true);assert.equal(help.getAttribute('aria-describedby'),'existing-help');
fire('pointerover',help);assert.equal(tip.element.hidden,true,'dismissed tooltip does not reopen on incidental pointer events');
fire('focusin',help);assert.equal(tip.element.hidden,false,'refocusing can reopen help');
fire('click',help);assert.equal(tip.element.hidden,false,'tap pins a help tooltip');fire('click',help);assert.equal(tip.element.hidden,true,'second tap closes help');
const action=new Element();action.dataset.tooltip='Edit account';fire('pointerover',action);
const click=fire('click',action);assert.equal(click.prevented,undefined);assert.equal(click.stopped,undefined,'normal actions retain their click behavior');assert.equal(tip.element.hidden,true);
help.dataset.tooltip='<script>untrusted text</script>';fire('focusin',help);assert.equal(tip.element.textContent,'译 <script>untrusted text</script>','tooltip accepts text, never HTML');
windowListeners.get('hashchange')();assert.equal(tip.element.hidden,true);assert.equal(help.getAttribute('aria-describedby'),'existing-help');
help.setAttribute('data-tooltip-literal','');help.dataset.tooltip='Search. Queue · Cache';
fire('focusin',help);assert.equal(tip.element.textContent,'Search. Queue · Cache','administrator resource text must not be translated as product copy');
console.log('PASS hover, keyboard, Escape, touch help, action behavior, viewport bounds and safe tooltip text');
