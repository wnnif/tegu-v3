import {createSessionGate, SessionChangedError} from './sessionAccess'
export {SessionChangedError} from './sessionAccess'
export const apiSession=createSessionGate()
let sessionExpired: (()=>void)|undefined
let sessionValidate: (()=>Promise<void>)|undefined
export function onSessionExpired(handler:()=>void){sessionExpired=handler;return ()=>{if(sessionExpired===handler)sessionExpired=undefined}}
export function onSessionValidate(handler:()=>Promise<void>){sessionValidate=handler;return ()=>{if(sessionValidate===handler)sessionValidate=undefined}}
export async function api<T = unknown>(path:string,body?:unknown,key?:string):Promise<T>{
 const revision=apiSession.capture()
 // Revalidate the same user's access before protected reads/mutations, not just after sign-in.
 if(sessionValidate&&!['/me','/login','/logout'].includes(path)){await sessionValidate();apiSession.assert(revision)}
 let response:Response
 try{
  response=await fetch('/api'+path,{method:body===undefined?'GET':'POST',credentials:'same-origin',headers:{'Content-Type':'application/json',...(key?{'Idempotency-Key':key}:{})},body:body===undefined?undefined:JSON.stringify(body)})
 }catch{
  apiSession.assert(revision)
  throw new Error('无法连接服务，请检查网络或稍后重试')
 }
 apiSession.assert(revision)
 const contentType=response.headers.get('content-type')||''
 let data:unknown=null
 if(contentType.includes('application/json')){
  try{data=await response.json()}catch{data=null}
 }
 apiSession.assert(revision)
 if(!response.ok){
  if(response.status===401&&path!=='/login'){sessionExpired?.();throw new SessionChangedError()}
  if(response.status===502||response.status===503||response.status===504)throw new Error('服务暂时不可用，请确认预览服务已启动后重试')
  throw new Error((typeof data==='object'&&data!==null&&'error' in data?String(data.error):'')||`请求失败（${response.status}）`)
 }
 if(data===null)throw new Error('服务返回了空响应，请稍后重试')
 return data as T
}
// Reuse a request key after a lost response; changed content gets a fresh key.
export function command(){let signature='',key='';return async(path:string,data:unknown)=>{const next=path+JSON.stringify(data);if(signature!==next){signature=next;key=Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('')}const result=await api(path,data,key);signature='';return result}}
