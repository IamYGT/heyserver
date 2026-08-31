import{i as e}from"./api-C4eSRsSJ.js";function t(e){let t=``,n=(n=!1)=>{t=t.replace(/\r\n/g,`
`);let r=t.indexOf(`

`);for(;r>=0;){let n=t.slice(0,r);t=t.slice(r+2);let i=n.split(`
`).filter(e=>e.startsWith(`data:`)).map(e=>e.slice(5).replace(/^ /,``)).join(`
`);i&&e(i),r=t.indexOf(`

`)}if(n&&t){let n=t.split(`
`).filter(e=>e.startsWith(`data:`)).map(e=>e.slice(5).replace(/^ /,``)).join(`
`);n&&e(n),t=``}};return{push(e){t+=e,n()},finish(){n(!0)}}}async function n(n,r,i,a){let o=await e(n,{signal:r,headers:{Accept:`text/event-stream`}});if(!o.ok)throw Error(`Stream request failed with HTTP ${o.status}`);if(!o.body)throw Error(`Streaming response body is unavailable`);i();let s=o.body.getReader(),c=new TextDecoder,l=t(a);for(;;){let{done:e,value:t}=await s.read();if(e)break;l.push(c.decode(t,{stream:!0}))}l.push(c.decode()),l.finish()}export{n as t};