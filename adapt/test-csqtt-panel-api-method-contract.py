#!/usr/bin/env python3
"""Check methods used by seven-tab CSQTT UI against Go API handlers.

Static source contract: no network, no runtime changes, no external credentials.
"""
from pathlib import Path
import re

PANEL=Path("adapt/csqtt-panel-preview.html")
BACKEND=Path("adapt/headless_main.go")
ROUTE_RX=re.compile(r'mux\.HandleFunc\("([^"]+)"')
REQUEST_RX=re.compile(r'\breq\s*\(')
LITERAL_RX=re.compile(r"^\s*(['\"])(/[^'\"]+)\1")
METHOD_RX=re.compile(r"^\s*(['\"])([A-Z]+)\1\s*$")
GO_METHOD_RX=re.compile(r'http\.Method(Get|Post|Put|Delete|Patch|Head|Options)\b')


def top_level_arguments(js: str, opening: int) -> list[str]:
    """Split a JS call without treating nested JS argument commas as separators."""
    args=[]
    arg_start=opening+1
    pairs={")":"(","]":"[","}":"{"}
    stack=["("]
    quoted=""
    escaped=False
    for idx in range(opening+1,len(js)):
        c=js[idx]
        if quoted:
            if escaped:
                escaped=False
            elif c=="\\":
                escaped=True
            elif c==quoted:
                quoted=""
            continue
        if c in ("'",'"',chr(96)):
            quoted=c
            continue
        if c in "([{":
            stack.append(c)
        elif c in ")]}":
            if not stack or stack[-1]!=pairs[c]:
                raise AssertionError("invalid or unsupported JS request call nesting")
            stack.pop()
            if not stack:
                args.append(js[arg_start:idx].strip())
                return args
        elif c=="," and len(stack)==1:
            args.append(js[arg_start:idx].strip())
            arg_start=idx+1
    raise AssertionError("unterminated JS request call")


def parse_frontend_requests(html: str) -> list[tuple[str,str,bool]]:
    calls=[]
    for match in REQUEST_RX.finditer(html):
        args=top_level_arguments(html,match.end()-1)
        if args[0]=="path":  # request helper definition
            continue
        path=LITERAL_RX.match(args[0])
        if not path:
            raise AssertionError("unrecognized CSQTT req() first argument; review dynamic route")
        prefix=path.group(2)
        dynamic=bool(args[0][path.end():].strip())
        if dynamic and not prefix.endswith("/"):
            raise AssertionError("dynamic path lacks explicit backend prefix")
        if len(args)>1:
            method=METHOD_RX.fullmatch(args[1])
            if not method:
                raise AssertionError(f"unknown API method argument for prefix {prefix}")
            verb=method.group(2)
        else:
            verb="GET"
        calls.append((prefix,verb,dynamic))
    if not calls:
        raise AssertionError("no UI API calls discovered")
    return calls


def backend_methods(source: str) -> dict[str,set[str]]:
    matches=list(ROUTE_RX.finditer(source))
    handlers={}
    for idx,m in enumerate(matches):
        end=matches[idx+1].start() if idx+1<len(matches) else source.find("\n    return mux",m.end())
        if end<0:
            end=len(source)
        body=source[m.end():end]
        verbs={n.upper() for n in GO_METHOD_RX.findall(body)}
        if not verbs:
            raise AssertionError(f"Go handler declares no HTTP verb: {m.group(1)}")
        handlers[m.group(1)]=verbs
    return handlers


def check(panel: str, backend: str) -> tuple[int,int]:
    front=parse_frontend_requests(panel)
    routes=backend_methods(backend)
    reviewed=set()
    for path,verb,dynamic in front:
        if path not in routes:
            raise AssertionError(f"missing Go route for native panel API path {path}")
        if verb not in routes[path]:
            raise AssertionError(
                f"wrong HTTP verb {verb} for {path}: Go handler accepts {sorted(routes[path])}")
        reviewed.add((path,verb))
    return len(front),len(reviewed)


def assert_rejects(frontend: str, backend: str, label: str) -> None:
    try:
        check(frontend,backend)
    except AssertionError:
        print(f"PASS mutation rejected: {label}")
    else:
        raise AssertionError("contract failed to detect method mismatch: "+label)


def main() -> None:
    ui=PANEL.read_text()
    go=BACKEND.read_text()
    count,unique=check(ui,go)
    assert count>=40 and unique>=30,(count,unique)
    # Static and dynamic paths both need enforceable method semantics.
    assert_rejects(
        ui.replace("req('/singbox/config','PUT'", "req('/singbox/config','POST'",1),
        go,"sing-box PUT incorrectly changed to POST")
    assert_rejects(
        ui.replace("req('/configs/'+encodeURIComponent(b.dataset.del),'DELETE'",
                   "req('/configs/'+encodeURIComponent(b.dataset.del),'GET'",1),
        go,"dynamic profile DELETE incorrectly changed to GET")
    assert_rejects(
        ui.replace("req('/singbox/rulesets/'+encodeURIComponent(tag),'DELETE'",
                   "req('/singbox/rulesets/'+encodeURIComponent(tag),'POST'",1),
        go,"dynamic Rule Set DELETE incorrectly changed to POST")
    assert_rejects(ui,go.replace('mux.HandleFunc("/singbox/status"',
                                 'mux.HandleFunc("/singbox/broken-status"',1),
                   "backend path removed")
    print(f"PASS CSQTT seven-tab UI/Go HTTP method contract: {count} calls, "
          f"{unique} unique path-method pairs, no network")


if __name__=="__main__":
    main()
