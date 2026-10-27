from pathlib import Path

out = Path(__file__).resolve().parents[1] / "go/redis/redis.go"
s = """// Package redis maps the selected application's Redis operations to native DO storage.
package redis
import("context";"errors";"fmt";"strconv";"time";"encoding/json";"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge")
var Nil=errors.New("redis: nil")
type Options struct {DialTimeout,ReadTimeout,WriteTimeout,PoolTimeout time.Duration;ContextTimeoutEnabled bool;MaxRetries int}
func ParseURL(string)(*Options,error){return &Options{},nil}
type Client struct{}
func NewClient(*Options)*Client{return &Client{}}
func(*Client)Close()error{return nil}
type Z struct{Score float64;Member any}
type ZRangeBy struct{Min,Max string;Offset,Count int64}
type cmd[T any] struct{v T;e error}
func(c *cmd[T])Result()(T,error){return c.v,c.e}
func(c *cmd[T])Val()T{return c.v}
func(c *cmd[T])Err()error{return c.e}
type IntCmd=cmd[int64]
type BoolCmd=cmd[bool]
type StatusCmd=cmd[string]
type FloatCmd=cmd[float64]
type SliceCmd=cmd[[]any]
type StringSliceCmd=cmd[[]string]
type ZSliceCmd=cmd[[]Z]
type StringCmd struct{cmd[string]}
func(c *StringCmd)Int64()(int64,error){if c.e!=nil{return 0,c.e};return strconv.ParseInt(c.v,10,64)}
func(c *StringCmd)Bytes()([]byte,error){return []byte(c.v),c.e}
type Cmder interface{Err()error}
func run[T any](ctx context.Context,op string,args ...any)*cmd[T]{var v *T;e:=bridge.Call(ctx,"redis",map[string]any{"op":op,"args":args},&v);if e==nil&&v==nil{e=Nil};c:=&cmd[T]{e:e};if v!=nil{c.v=*v};return c}
func str(ctx context.Context,op string,args ...any)*StringCmd{return &StringCmd{*run[string](ctx,op,args...)}}
func value(v any)string{switch x:=v.(type){case []byte:return string(x);case string:return x;default:return fmt.Sprint(v)}}
func(*Client)Pipeline()*Pipeline{return &Pipeline{}}
type Pipeline struct{pending []func()Cmder}
func(p *Pipeline)Len()int{return len(p.pending)}
func(p *Pipeline)Exec(context.Context)([]Cmder,error){a:=[]Cmder{};var e error;for _,f:=range p.pending{c:=f();a=append(a,c);if c.Err()!=nil&&e==nil{e=c.Err()}};p.pending=nil;return a,e}
type Script struct{s string}
func NewScript(s string)*Script{return &Script{s}}
type Cmd struct{cmd[json.RawMessage]}
func(c *Cmd)Int64()(int64,error){var n int64;e:=json.Unmarshal(c.v,&n);if c.e!=nil{e=c.e};return n,e}
func(c *Cmd)Slice()([]any,error){var a []int64;e:=json.Unmarshal(c.v,&a);r:=make([]any,len(a));for i,n:=range a{r[i]=n};if c.e!=nil{e=c.e};return r,e}
func(s *Script)Run(ctx context.Context,c *Client,keys []string,args ...any)*Cmd{return &Cmd{*run[json.RawMessage](ctx,"script",s.s,keys,args)}}
type ScanCmd struct{cmd[[]string]}
type ScanIterator struct{keys []string;i int;e error}
func(c *ScanCmd)Iterator()*ScanIterator{return &ScanIterator{keys:c.v,i:-1,e:c.e}}
func(i *ScanIterator)Next(context.Context)bool{i.i++;return i.i<len(i.keys)}
func(i *ScanIterator)Val()string{return i.keys[i.i]}
func(i *ScanIterator)Err()error{return i.e}
"""
methods = [
    ("Ping", "", "StatusCmd", "string", ""),
    ("Get", "key string", "StringCmd", "string", "key"),
    (
        "Set",
        "key string,v any,ttl time.Duration",
        "StatusCmd",
        "string",
        "key,value(v),ttl.Milliseconds()",
    ),
    (
        "SetNX",
        "key string,v any,ttl time.Duration",
        "BoolCmd",
        "bool",
        "key,value(v),ttl.Milliseconds()",
    ),
    ("Del", "keys ...string", "IntCmd", "int64", "keys"),
    (
        "Expire",
        "key string,ttl time.Duration",
        "BoolCmd",
        "bool",
        "key,ttl.Milliseconds()",
    ),
    ("Incr", "key string", "IntCmd", "int64", "key"),
    ("MGet", "keys ...string", "SliceCmd", "[]any", "keys"),
    ("LPush", "key string,v ...any", "IntCmd", "int64", "key,v"),
    (
        "BRPop",
        "timeout time.Duration,keys ...string",
        "StringSliceCmd",
        "[]string",
        "keys,timeout.Milliseconds()",
    ),
    ("ZAdd", "key string,z ...Z", "IntCmd", "int64", "key,z"),
    ("ZRem", "key string,members ...any", "IntCmd", "int64", "key,members"),
    ("ZRemRangeByScore", "key,min,max string", "IntCmd", "int64", "key,min,max"),
    ("ZRangeByScore", "key string,r *ZRangeBy", "StringSliceCmd", "[]string", "key,r"),
    ("ZCount", "key,min,max string", "IntCmd", "int64", "key,min,max"),
    (
        "ZIncrBy",
        "key string,n float64,member string",
        "FloatCmd",
        "float64",
        "key,n,member",
    ),
    (
        "ZRevRangeWithScores",
        "key string,start,stop int64",
        "ZSliceCmd",
        "[]Z",
        "key,start,stop",
    ),
    ("HSet", "key string,v ...any", "IntCmd", "int64", "key,v"),
    ("HDel", "key string,fields ...string", "IntCmd", "int64", "key,fields"),
    ("HMGet", "key string,fields ...string", "SliceCmd", "[]any", "key,fields"),
    ("PFAdd", "key string,v ...any", "IntCmd", "int64", "key,v"),
    ("PFCount", "keys ...string", "IntCmd", "int64", "keys"),
    ("Scan", "cursor uint64,match string,count int64", "ScanCmd", "[]string", "match"),
]
# Commands are typed exactly as go-redis; pipeline results settle during Exec.
for name, params, ret, typ, args in methods:
    sig = "ctx context.Context" + ("," + params if params else "")
    call = "str" if ret == "StringCmd" else f"run[{typ}]"
    body = f'{call}(ctx,"{name}"' + ("," + args if args else "") + ")"
    if ret == "ScanCmd":
        body = "&ScanCmd{*" + body + "}"
    s += f"func(c *Client){name}({sig})*{ret}" + "{return " + body + "}\n"
    s += (
        f"func(p *Pipeline){name}({sig})*{ret}"
        + "{result:=&"
        + ret
        + "{};p.pending=append(p.pending,func()Cmder{x:="
        + body
        + ";*result=*x;return result});return result}\n"
    )
out.write_text(s)

import subprocess

subprocess.run(["gofmt", "-w", str(out)], check=True)
