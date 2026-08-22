#include "runtime_internal.h"

static void put_char(char* b, size_t cap, size_t* p, char c) { if (*p + 1 < cap) b[(*p)++] = c; }
static void put_str(char* b, size_t cap, size_t* p, const char* s) { if (!s) return; while (*s && *p + 1 < cap) b[(*p)++] = *s++; }
static void put_u64(char* b, size_t cap, size_t* p, uint64_t v, int hex) {
    char tmp[32]; int n = 0; uint32_t base = hex ? 16 : 10;
    do { uint32_t d=(uint32_t)(v%base); tmp[n++]=(char)(d<10?'0'+d:'A'+d-10); v/=base; } while(v && n<32);
    while(n) put_char(b,cap,p,tmp[--n]);
}
static void put_i64(char* b,size_t cap,size_t* p,int64_t v){ if(v<0){put_char(b,cap,p,'-');put_u64(b,cap,p,(uint64_t)(-(v+1))+1u,0);}else put_u64(b,cap,p,(uint64_t)v,0); }
static void put_json(char* b,size_t cap,size_t* p,const char* s){ put_char(b,cap,p,'"'); if(s){while(*s&&*p+8<cap){unsigned char c=(unsigned char)*s++;if(c=='"'||c=='\\'){put_char(b,cap,p,'\\');put_char(b,cap,p,(char)c);}else if(c=='\n')put_str(b,cap,p,"\\n");else if(c=='\r')put_str(b,cap,p,"\\r");else if(c=='\t')put_str(b,cap,p,"\\t");else if(c>=0x20)put_char(b,cap,p,(char)c);}} put_char(b,cap,p,'"'); }
static void put_hex_bytes(char* b,size_t cap,size_t* p,const BYTE* bytes,uint32_t count){static const char* h="0123456789ABCDEF";for(uint32_t i=0;i<count;++i){put_char(b,cap,p,h[(bytes[i]>>4)&15]);put_char(b,cap,p,h[bytes[i]&15]);}}
static const char* confidence_name(RmlSymbolConfidence c){switch(c){case RML_SYMBOL_CONFIRMED:return"CONFIRMED";case RML_SYMBOL_PROBABLE:return"PROBABLE";case RML_SYMBOL_AMBIGUOUS:return"AMBIGUOUS";case RML_SYMBOL_INCOMPATIBLE:return"INCOMPATIBLE";case RML_SYMBOL_OBSERVED:return"OBSERVED";default:return"UNRESOLVED";}}

void write_runtime_report(uint32_t bootstrap_status) {
    const size_t cap=768*1024; char* b=(char*)rml_alloc(cap); if(!b)return; size_t p=0;
    uintptr_t image_base=(uintptr_t)GetModuleHandleW(0);
    put_str(b,cap,&p,"{\r\n  \"schemaVersion\": 4,\r\n  \"runtimeVersion\": "); put_json(b,cap,&p,RML_RUNTIME_VERSION);
    put_str(b,cap,&p,",\r\n  \"loaderApi\": ");put_u64(b,cap,&p,RML_API_VERSION,0);
    put_str(b,cap,&p,",\r\n  \"bootstrapStatus\": ");put_u64(b,cap,&p,bootstrap_status,0);
    put_str(b,cap,&p,",\r\n  \"sessionId\": ");put_json(b,cap,&p,g_plan.session_id);
    put_str(b,cap,&p,",\r\n  \"launchProfile\": ");put_json(b,cap,&p,plan_profile_short());
    put_str(b,cap,&p,",\r\n  \"game\": {\"version\": ");put_json(b,cap,&p,g_plan.game_version);put_str(b,cap,&p,", \"sha256\": ");put_json(b,cap,&p,g_plan.game_sha256);put_str(b,cap,&p,", \"imageBase\": \"0x");put_u64(b,cap,&p,image_base,1);put_str(b,cap,&p,"\"},");

    put_str(b,cap,&p,"\r\n  \"symbolCache\": {\"state\": ");put_json(b,cap,&p,g_symbol_cache_stats.state);put_str(b,cap,&p,", \"cachedSymbols\": ");put_u64(b,cap,&p,g_symbol_cache_stats.cache_supplied,0);put_str(b,cap,&p,", \"validatedFromCache\": ");put_u64(b,cap,&p,g_symbol_cache_stats.cache_validated,0);put_str(b,cap,&p,", \"fullScans\": ");put_u64(b,cap,&p,g_symbol_cache_stats.full_resolutions,0);put_str(b,cap,&p,", \"resolutionMicros\": ");put_u64(b,cap,&p,(uint64_t)(g_symbol_cache_stats.validation_ms+g_symbol_cache_stats.full_resolution_ms)*1000ull,0);put_str(b,cap,&p,"},");

    uint32_t legacy_hooks=0; for(uint32_t i=0;i<g_plugin_count;++i)if(g_plugins[i].flags&PLUGIN_TESMIO_LEGACY)legacy_hooks+=g_plugins[i].legacy_hooks_installed;
    const char* steady=legacy_hooks?"LEGACY PLUGIN HOOK ACTIVE":"NONE";
    put_str(b,cap,&p,"\r\n  \"performance\": {\"steadyStateWork\": ");put_json(b,cap,&p,steady);put_str(b,cap,&p,", \"workerThreads\": ");put_u64(b,cap,&p,g_performance_stats.worker_threads,0);put_str(b,cap,&p,", \"timers\": ");put_u64(b,cap,&p,g_performance_stats.timers,0);put_str(b,cap,&p,", \"pollingLoops\": ");put_u64(b,cap,&p,g_performance_stats.polling_loops,0);put_str(b,cap,&p,", \"bootstrapMs\": ");put_u64(b,cap,&p,g_performance_stats.bootstrap_ms,0);put_str(b,cap,&p,"},");

    put_str(b,cap,&p,"\r\n  \"symbols\": [\r\n");
    for(uint32_t i=0;i<g_symbol_count;++i){SymbolRecord* s=symbol_registry_at(i);if(!s)continue;put_str(b,cap,&p,"    {\"id\": ");put_json(b,cap,&p,s->id);put_str(b,cap,&p,", \"displayName\": ");put_json(b,cap,&p,s->display_name?s->display_name:s->id);put_str(b,cap,&p,", \"address\": \"0x");put_u64(b,cap,&p,s->address,1);put_str(b,cap,&p,"\", \"rva\": ");put_u64(b,cap,&p,s->rva,0);put_str(b,cap,&p,", \"confidence\": ");put_json(b,cap,&p,confidence_name(s->confidence));put_str(b,cap,&p,", \"resolver\": ");put_json(b,cap,&p,s->resolver);put_str(b,cap,&p,", \"evidence\": ");put_json(b,cap,&p,s->evidence);put_str(b,cap,&p,", \"xrefCount\": ");put_u64(b,cap,&p,s->xref_count,0);put_str(b,cap,&p,", \"functionSize\": ");put_u64(b,cap,&p,s->function_size,0);put_str(b,cap,&p,", \"entryFingerprint\": \"");put_hex_bytes(b,cap,&p,s->entry_fingerprint,s->entry_fingerprint_length);put_str(b,cap,&p,"\", \"source\": ");put_json(b,cap,&p,s->from_symbol_cache?"symbol-cache":(s->observed_only?"runtime-observation":"runtime-resolution"));put_str(b,cap,&p,", \"observedOnly\": ");put_str(b,cap,&p,s->observed_only?"true":"false");put_str(b,cap,&p,", \"temperature\": ");put_json(b,cap,&p,s->temperature);put_char(b,cap,&p,'}');put_str(b,cap,&p,i+1<g_symbol_count?",\r\n":"\r\n");}
    put_str(b,cap,&p,"  ],");

    put_str(b,cap,&p,"\r\n  \"services\": {\"count\": ");put_u64(b,cap,&p,g_service_count,0);put_str(b,cap,&p,"},");
    put_str(b,cap,&p,"\r\n  \"deepTest\": {\"active\": ");put_str(b,cap,&p,g_deep_test_stats.active?"true":"false");put_str(b,cap,&p,", \"pluginsObserved\": ");put_u64(b,cap,&p,g_deep_test_stats.plugins_observed,0);put_str(b,cap,&p,", \"hostHookRequests\": ");put_u64(b,cap,&p,g_deep_test_stats.host_hook_requests,0);put_str(b,cap,&p,", \"unmanagedTextChanges\": ");put_u64(b,cap,&p,g_deep_test_stats.unmanaged_text_changes,0);put_str(b,cap,&p,", \"unmanagedTextPages\": ");put_u64(b,cap,&p,g_deep_test_stats.unmanaged_text_pages,0);put_str(b,cap,&p,", \"monitorScans\": ");put_u64(b,cap,&p,g_deep_test_stats.monitor_scans,0);put_str(b,cap,&p,", \"lateChangeWindows\": ");put_u64(b,cap,&p,g_deep_test_stats.late_change_windows,0);put_str(b,cap,&p,", \"lastAuditIssues\": ");put_u64(b,cap,&p,g_deep_test_stats.last_audit_issues,0);put_str(b,cap,&p,"},");

    put_str(b,cap,&p,"\r\n  \"hookChains\": [\r\n");
    uint32_t chain_count=tesmio_compat_chain_count();
    LegacyHookChainSnapshot* chain=(LegacyHookChainSnapshot*)rml_alloc(sizeof(LegacyHookChainSnapshot));
    uint32_t emitted_chains=0;
    if(chain){for(uint32_t i=0;i<chain_count;++i){if(!tesmio_compat_chain_snapshot(i,chain))continue;if(emitted_chains++)put_str(b,cap,&p,",\r\n");
        put_str(b,cap,&p,"    {\"type\": ");put_json(b,cap,&p,chain->type);put_str(b,cap,&p,", \"target\": ");put_json(b,cap,&p,chain->target_desc);put_str(b,cap,&p,", \"symbolId\": ");put_json(b,cap,&p,chain->symbol_id);put_str(b,cap,&p,", \"rva\": ");put_u64(b,cap,&p,chain->rva,0);put_str(b,cap,&p,", \"stolenBytes\": ");put_u64(b,cap,&p,chain->stolen,0);put_str(b,cap,&p,", \"integrity\": ");put_json(b,cap,&p,chain->integrity);put_str(b,cap,&p,", \"proof\": ");put_json(b,cap,&p,chain->proof);put_str(b,cap,&p,", \"effectiveTop\": \"0x");put_u64(b,cap,&p,chain->effective_top,1);put_str(b,cap,&p,"\", \"currentDestination\": \"0x");put_u64(b,cap,&p,chain->current_destination,1);put_str(b,cap,&p,"\", \"original\": \"0x");put_u64(b,cap,&p,chain->original,1);put_str(b,cap,&p,"\", \"observedTopOwner\": ");put_json(b,cap,&p,chain->observed_top_owner);put_str(b,cap,&p,", \"steps\": [");
        for(uint32_t j=0;j<chain->step_count;++j){LegacyHookChainStepSnapshot* st=&chain->steps[j];put_str(b,cap,&p,"{\"pluginId\": ");put_json(b,cap,&p,st->plugin_id);put_str(b,cap,&p,", \"pluginSha256\": ");put_json(b,cap,&p,st->plugin_sha256);put_str(b,cap,&p,", \"label\": ");put_json(b,cap,&p,st->label);put_str(b,cap,&p,", \"detour\": \"0x");put_u64(b,cap,&p,st->detour,1);put_str(b,cap,&p,"\", \"continuation\": \"0x");put_u64(b,cap,&p,st->continuation,1);put_str(b,cap,&p,"\", \"previousEffectiveTop\": \"0x");put_u64(b,cap,&p,st->previous_effective_top,1);put_str(b,cap,&p,"\", \"originalTrampoline\": \"0x");put_u64(b,cap,&p,st->original_trampoline,1);put_str(b,cap,&p,"\", \"stolenBytes\": ");put_u64(b,cap,&p,st->stolen_bytes,0);put_str(b,cap,&p,", \"installOrder\": ");put_u64(b,cap,&p,st->install_order,0);put_str(b,cap,&p,", \"executionOrder\": ");put_u64(b,cap,&p,st->execution_order,0);put_char(b,cap,&p,'}');if(j+1<chain->step_count)put_char(b,cap,&p,',');}
        put_str(b,cap,&p,"]}");}
        rml_free(chain);
    }
    put_str(b,cap,&p,"\r\n  ],");

    put_str(b,cap,&p,"\r\n  \"plugins\": [\r\n");
    for(uint32_t i=0;i<g_plugin_count;++i){PluginState* pl=&g_plugins[i];put_str(b,cap,&p,"    {\"id\": ");put_json(b,cap,&p,pl->id);put_str(b,cap,&p,", \"name\": ");put_json(b,cap,&p,pl->name[0]?pl->name:pl->id);put_str(b,cap,&p,", \"version\": ");put_json(b,cap,&p,pl->version);put_str(b,cap,&p,", \"sha256\": ");put_json(b,cap,&p,pl->dll_sha256);put_str(b,cap,&p,", \"priority\": ");put_i64(b,cap,&p,pl->priority);put_str(b,cap,&p,", \"loadOrder\": ");put_u64(b,cap,&p,i+1u,0);put_str(b,cap,&p,", \"forced\": ");put_str(b,cap,&p,(pl->flags&PLUGIN_FORCED)?"true":"false");put_str(b,cap,&p,", \"loaderType\": ");put_json(b,cap,&p,(pl->flags&PLUGIN_TESMIO_LEGACY)?"TESMIO LEGACY":((pl->flags&PLUGIN_RAW_NATIVE)?"RAW NATIVE DLL":"RML NATIVE"));put_str(b,cap,&p,", \"legacyApi\": ");put_u64(b,cap,&p,pl->legacy_api,0);put_str(b,cap,&p,", \"legacyHooks\": ");put_u64(b,cap,&p,pl->legacy_hooks_installed,0);put_str(b,cap,&p,", \"legacyHookFailures\": ");put_u64(b,cap,&p,pl->legacy_hook_failures,0);put_str(b,cap,&p,", \"legacyServices\": ");put_u64(b,cap,&p,pl->legacy_services_provided,0);put_str(b,cap,&p,", \"unmanagedInitChanges\": ");put_u64(b,cap,&p,pl->unmanaged_init_changes,0);put_str(b,cap,&p,", \"unmanagedStartChanges\": ");put_u64(b,cap,&p,pl->unmanaged_start_changes,0);put_str(b,cap,&p,", \"unmanagedInitPages\": ");put_u64(b,cap,&p,pl->unmanaged_init_pages,0);put_str(b,cap,&p,", \"unmanagedStartPages\": ");put_u64(b,cap,&p,pl->unmanaged_start_pages,0);put_str(b,cap,&p,", \"firstUnmanagedRva\": ");put_u64(b,cap,&p,pl->first_unmanaged_rva,0);put_str(b,cap,&p,", \"loaded\": ");put_str(b,cap,&p,pl->loaded?"true":"false");put_str(b,cap,&p,", \"failed\": ");put_str(b,cap,&p,pl->failed?"true":"false");put_str(b,cap,&p,", \"status\": ");put_json(b,cap,&p,pl->status);put_char(b,cap,&p,'}');put_str(b,cap,&p,i+1<g_plugin_count?",\r\n":"\r\n");}
    put_str(b,cap,&p,"  ]\r\n}\r\n");b[p]=0;

    wchar_t path[RML_MAX_PATH];if(rml_join_path(path,RML_MAX_PATH,g_root_path,L"rml\\state\\last_runtime_report.json")){HANDLE f=CreateFileW(path,GENERIC_WRITE,FILE_SHARE_READ,0,CREATE_ALWAYS,FILE_ATTRIBUTE_NORMAL,0);if(f!=INVALID_HANDLE_VALUE){DWORD written=0;WriteFile(f,b,(DWORD)p,&written,0);CloseHandle(f);}}
    rml_free(b);
}
