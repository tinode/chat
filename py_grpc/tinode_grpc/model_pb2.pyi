from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

ACCEPT: CallEvent
ANON: AuthLevel
ANSWER: CallEvent
AUTH: AuthLevel
CALL: InfoNote
CONTINUE: RespCode
CREATE: Crud
DELETE: Crud
DESCRIPTOR: _descriptor.FileDescriptor
DROP: RespCode
HANG_UP: CallEvent
ICE_CANDIDATE: CallEvent
INVITE: CallEvent
KP: InfoNote
NONE: AuthLevel
OFFER: CallEvent
READ: InfoNote
RECV: InfoNote
REPLACE: RespCode
RESPOND: RespCode
RINGING: CallEvent
ROOT: AuthLevel
UPDATE: Crud
X1: InfoNote
X2: CallEvent

class AccessMode(_message.Message):
    __slots__ = ["given", "want"]
    GIVEN_FIELD_NUMBER: _ClassVar[int]
    WANT_FIELD_NUMBER: _ClassVar[int]
    given: str
    want: str
    def __init__(self, want: _Optional[str] = ..., given: _Optional[str] = ...) -> None: ...

class AccountEvent(_message.Message):
    __slots__ = ["action", "default_acs", "public", "tags", "user_id"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_ACS_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    default_acs: DefaultAcsMode
    public: bytes
    tags: _containers.RepeatedScalarFieldContainer[str]
    user_id: str
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., user_id: _Optional[str] = ..., default_acs: _Optional[_Union[DefaultAcsMode, _Mapping]] = ..., public: _Optional[bytes] = ..., tags: _Optional[_Iterable[str]] = ...) -> None: ...

class ClientAcc(_message.Message):
    __slots__ = ["auth_level", "cred", "desc", "id", "login", "scheme", "secret", "state", "tags", "tmp_scheme", "tmp_secret", "token", "user_id"]
    AUTH_LEVEL_FIELD_NUMBER: _ClassVar[int]
    CRED_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    LOGIN_FIELD_NUMBER: _ClassVar[int]
    SCHEME_FIELD_NUMBER: _ClassVar[int]
    SECRET_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    TMP_SCHEME_FIELD_NUMBER: _ClassVar[int]
    TMP_SECRET_FIELD_NUMBER: _ClassVar[int]
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    auth_level: AuthLevel
    cred: _containers.RepeatedCompositeFieldContainer[ClientCred]
    desc: SetDesc
    id: str
    login: bool
    scheme: str
    secret: bytes
    state: str
    tags: _containers.RepeatedScalarFieldContainer[str]
    tmp_scheme: str
    tmp_secret: bytes
    token: bytes
    user_id: str
    def __init__(self, id: _Optional[str] = ..., user_id: _Optional[str] = ..., scheme: _Optional[str] = ..., secret: _Optional[bytes] = ..., login: bool = ..., tags: _Optional[_Iterable[str]] = ..., desc: _Optional[_Union[SetDesc, _Mapping]] = ..., cred: _Optional[_Iterable[_Union[ClientCred, _Mapping]]] = ..., token: _Optional[bytes] = ..., state: _Optional[str] = ..., auth_level: _Optional[_Union[AuthLevel, str]] = ..., tmp_scheme: _Optional[str] = ..., tmp_secret: _Optional[bytes] = ...) -> None: ...

class ClientCred(_message.Message):
    __slots__ = ["method", "params", "response", "value"]
    class ParamsEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    METHOD_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    method: str
    params: _containers.ScalarMap[str, bytes]
    response: str
    value: str
    def __init__(self, method: _Optional[str] = ..., value: _Optional[str] = ..., response: _Optional[str] = ..., params: _Optional[_Mapping[str, bytes]] = ...) -> None: ...

class ClientDel(_message.Message):
    __slots__ = ["cred", "del_seq", "hard", "id", "topic", "user_id", "what"]
    class What(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = []
    CRED: ClientDel.What
    CRED_FIELD_NUMBER: _ClassVar[int]
    DEL_SEQ_FIELD_NUMBER: _ClassVar[int]
    HARD_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    MSG: ClientDel.What
    SUB: ClientDel.What
    TOPIC: ClientDel.What
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    USER: ClientDel.What
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    X0: ClientDel.What
    cred: ClientCred
    del_seq: _containers.RepeatedCompositeFieldContainer[SeqRange]
    hard: bool
    id: str
    topic: str
    user_id: str
    what: ClientDel.What
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., what: _Optional[_Union[ClientDel.What, str]] = ..., del_seq: _Optional[_Iterable[_Union[SeqRange, _Mapping]]] = ..., user_id: _Optional[str] = ..., cred: _Optional[_Union[ClientCred, _Mapping]] = ..., hard: bool = ...) -> None: ...

class ClientExtra(_message.Message):
    __slots__ = ["attachments", "auth_level", "on_behalf_of"]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    AUTH_LEVEL_FIELD_NUMBER: _ClassVar[int]
    ON_BEHALF_OF_FIELD_NUMBER: _ClassVar[int]
    attachments: _containers.RepeatedScalarFieldContainer[str]
    auth_level: AuthLevel
    on_behalf_of: str
    def __init__(self, attachments: _Optional[_Iterable[str]] = ..., on_behalf_of: _Optional[str] = ..., auth_level: _Optional[_Union[AuthLevel, str]] = ...) -> None: ...

class ClientGet(_message.Message):
    __slots__ = ["id", "query", "topic"]
    ID_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    id: str
    query: GetQuery
    topic: str
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., query: _Optional[_Union[GetQuery, _Mapping]] = ...) -> None: ...

class ClientHi(_message.Message):
    __slots__ = ["background", "device_id", "id", "lang", "platform", "user_agent", "ver"]
    BACKGROUND_FIELD_NUMBER: _ClassVar[int]
    DEVICE_ID_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    LANG_FIELD_NUMBER: _ClassVar[int]
    PLATFORM_FIELD_NUMBER: _ClassVar[int]
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    VER_FIELD_NUMBER: _ClassVar[int]
    background: bool
    device_id: str
    id: str
    lang: str
    platform: str
    user_agent: str
    ver: str
    def __init__(self, id: _Optional[str] = ..., user_agent: _Optional[str] = ..., ver: _Optional[str] = ..., device_id: _Optional[str] = ..., lang: _Optional[str] = ..., platform: _Optional[str] = ..., background: bool = ...) -> None: ...

class ClientLeave(_message.Message):
    __slots__ = ["id", "topic", "unsub"]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    UNSUB_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    unsub: bool
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., unsub: bool = ...) -> None: ...

class ClientLogin(_message.Message):
    __slots__ = ["cred", "id", "scheme", "secret"]
    CRED_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    SCHEME_FIELD_NUMBER: _ClassVar[int]
    SECRET_FIELD_NUMBER: _ClassVar[int]
    cred: _containers.RepeatedCompositeFieldContainer[ClientCred]
    id: str
    scheme: str
    secret: bytes
    def __init__(self, id: _Optional[str] = ..., scheme: _Optional[str] = ..., secret: _Optional[bytes] = ..., cred: _Optional[_Iterable[_Union[ClientCred, _Mapping]]] = ...) -> None: ...

class ClientMsg(_message.Message):
    __slots__ = ["acc", "extra", "get", "hi", "leave", "login", "note", "pub", "set", "sub"]
    ACC_FIELD_NUMBER: _ClassVar[int]
    DEL_FIELD_NUMBER: _ClassVar[int]
    EXTRA_FIELD_NUMBER: _ClassVar[int]
    GET_FIELD_NUMBER: _ClassVar[int]
    HI_FIELD_NUMBER: _ClassVar[int]
    LEAVE_FIELD_NUMBER: _ClassVar[int]
    LOGIN_FIELD_NUMBER: _ClassVar[int]
    NOTE_FIELD_NUMBER: _ClassVar[int]
    PUB_FIELD_NUMBER: _ClassVar[int]
    SET_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    acc: ClientAcc
    extra: ClientExtra
    get: ClientGet
    hi: ClientHi
    leave: ClientLeave
    login: ClientLogin
    note: ClientNote
    pub: ClientPub
    set: ClientSet
    sub: ClientSub
    def __init__(self, hi: _Optional[_Union[ClientHi, _Mapping]] = ..., acc: _Optional[_Union[ClientAcc, _Mapping]] = ..., login: _Optional[_Union[ClientLogin, _Mapping]] = ..., sub: _Optional[_Union[ClientSub, _Mapping]] = ..., leave: _Optional[_Union[ClientLeave, _Mapping]] = ..., pub: _Optional[_Union[ClientPub, _Mapping]] = ..., get: _Optional[_Union[ClientGet, _Mapping]] = ..., set: _Optional[_Union[ClientSet, _Mapping]] = ..., note: _Optional[_Union[ClientNote, _Mapping]] = ..., extra: _Optional[_Union[ClientExtra, _Mapping]] = ..., **kwargs) -> None: ...

class ClientNote(_message.Message):
    __slots__ = ["event", "payload", "seq_id", "topic", "unread", "what"]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    UNREAD_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    event: CallEvent
    payload: bytes
    seq_id: int
    topic: str
    unread: int
    what: InfoNote
    def __init__(self, topic: _Optional[str] = ..., what: _Optional[_Union[InfoNote, str]] = ..., seq_id: _Optional[int] = ..., unread: _Optional[int] = ..., event: _Optional[_Union[CallEvent, str]] = ..., payload: _Optional[bytes] = ...) -> None: ...

class ClientPub(_message.Message):
    __slots__ = ["content", "head", "id", "no_echo", "topic"]
    class HeadEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    NO_ECHO_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    content: bytes
    head: _containers.ScalarMap[str, bytes]
    id: str
    no_echo: bool
    topic: str
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., no_echo: bool = ..., head: _Optional[_Mapping[str, bytes]] = ..., content: _Optional[bytes] = ...) -> None: ...

class ClientReq(_message.Message):
    __slots__ = ["msg", "sess"]
    MSG_FIELD_NUMBER: _ClassVar[int]
    SESS_FIELD_NUMBER: _ClassVar[int]
    msg: ClientMsg
    sess: Session
    def __init__(self, msg: _Optional[_Union[ClientMsg, _Mapping]] = ..., sess: _Optional[_Union[Session, _Mapping]] = ...) -> None: ...

class ClientSet(_message.Message):
    __slots__ = ["id", "query", "topic"]
    ID_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    id: str
    query: SetQuery
    topic: str
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., query: _Optional[_Union[SetQuery, _Mapping]] = ...) -> None: ...

class ClientSub(_message.Message):
    __slots__ = ["get_query", "id", "set_query", "topic"]
    GET_QUERY_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    SET_QUERY_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    get_query: GetQuery
    id: str
    set_query: SetQuery
    topic: str
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., set_query: _Optional[_Union[SetQuery, _Mapping]] = ..., get_query: _Optional[_Union[GetQuery, _Mapping]] = ...) -> None: ...

class DefaultAcsMode(_message.Message):
    __slots__ = ["anon", "auth"]
    ANON_FIELD_NUMBER: _ClassVar[int]
    AUTH_FIELD_NUMBER: _ClassVar[int]
    anon: str
    auth: str
    def __init__(self, auth: _Optional[str] = ..., anon: _Optional[str] = ...) -> None: ...

class DelValues(_message.Message):
    __slots__ = ["del_id", "del_seq"]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_SEQ_FIELD_NUMBER: _ClassVar[int]
    del_id: int
    del_seq: _containers.RepeatedCompositeFieldContainer[SeqRange]
    def __init__(self, del_id: _Optional[int] = ..., del_seq: _Optional[_Iterable[_Union[SeqRange, _Mapping]]] = ...) -> None: ...

class GetOpts(_message.Message):
    __slots__ = ["before_id", "if_modified_since", "limit", "since_id", "topic", "user"]
    BEFORE_ID_FIELD_NUMBER: _ClassVar[int]
    IF_MODIFIED_SINCE_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    SINCE_ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    USER_FIELD_NUMBER: _ClassVar[int]
    before_id: int
    if_modified_since: int
    limit: int
    since_id: int
    topic: str
    user: str
    def __init__(self, if_modified_since: _Optional[int] = ..., user: _Optional[str] = ..., topic: _Optional[str] = ..., since_id: _Optional[int] = ..., before_id: _Optional[int] = ..., limit: _Optional[int] = ...) -> None: ...

class GetQuery(_message.Message):
    __slots__ = ["data", "desc", "sub", "what"]
    DATA_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    data: GetOpts
    desc: GetOpts
    sub: GetOpts
    what: str
    def __init__(self, what: _Optional[str] = ..., desc: _Optional[_Union[GetOpts, _Mapping]] = ..., sub: _Optional[_Union[GetOpts, _Mapping]] = ..., data: _Optional[_Union[GetOpts, _Mapping]] = ...) -> None: ...

class MessageEvent(_message.Message):
    __slots__ = ["action", "msg"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    MSG_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    msg: ServerData
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., msg: _Optional[_Union[ServerData, _Mapping]] = ...) -> None: ...

class SearchFound(_message.Message):
    __slots__ = ["query", "result", "status"]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    RESULT_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    query: str
    result: _containers.RepeatedCompositeFieldContainer[TopicSub]
    status: RespCode
    def __init__(self, status: _Optional[_Union[RespCode, str]] = ..., query: _Optional[str] = ..., result: _Optional[_Iterable[_Union[TopicSub, _Mapping]]] = ...) -> None: ...

class SearchQuery(_message.Message):
    __slots__ = ["query", "user_id"]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    query: str
    user_id: str
    def __init__(self, user_id: _Optional[str] = ..., query: _Optional[str] = ...) -> None: ...

class SeqRange(_message.Message):
    __slots__ = ["hi", "low"]
    HI_FIELD_NUMBER: _ClassVar[int]
    LOW_FIELD_NUMBER: _ClassVar[int]
    hi: int
    low: int
    def __init__(self, low: _Optional[int] = ..., hi: _Optional[int] = ...) -> None: ...

class ServerCred(_message.Message):
    __slots__ = ["done", "method", "value"]
    DONE_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    done: bool
    method: str
    value: str
    def __init__(self, method: _Optional[str] = ..., value: _Optional[str] = ..., done: bool = ...) -> None: ...

class ServerCtrl(_message.Message):
    __slots__ = ["code", "id", "params", "text", "topic"]
    class ParamsEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    CODE_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    code: int
    id: str
    params: _containers.ScalarMap[str, bytes]
    text: str
    topic: str
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., code: _Optional[int] = ..., text: _Optional[str] = ..., params: _Optional[_Mapping[str, bytes]] = ...) -> None: ...

class ServerData(_message.Message):
    __slots__ = ["content", "deleted_at", "from_user_id", "head", "seq_id", "timestamp", "topic"]
    class HeadEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    DELETED_AT_FIELD_NUMBER: _ClassVar[int]
    FROM_USER_ID_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    content: bytes
    deleted_at: int
    from_user_id: str
    head: _containers.ScalarMap[str, bytes]
    seq_id: int
    timestamp: int
    topic: str
    def __init__(self, topic: _Optional[str] = ..., from_user_id: _Optional[str] = ..., timestamp: _Optional[int] = ..., deleted_at: _Optional[int] = ..., seq_id: _Optional[int] = ..., head: _Optional[_Mapping[str, bytes]] = ..., content: _Optional[bytes] = ...) -> None: ...

class ServerInfo(_message.Message):
    __slots__ = ["event", "from_user_id", "payload", "seq_id", "src", "topic", "what"]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    FROM_USER_ID_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    SRC_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    event: CallEvent
    from_user_id: str
    payload: bytes
    seq_id: int
    src: str
    topic: str
    what: InfoNote
    def __init__(self, topic: _Optional[str] = ..., from_user_id: _Optional[str] = ..., what: _Optional[_Union[InfoNote, str]] = ..., seq_id: _Optional[int] = ..., src: _Optional[str] = ..., event: _Optional[_Union[CallEvent, str]] = ..., payload: _Optional[bytes] = ...) -> None: ...

class ServerMeta(_message.Message):
    __slots__ = ["cred", "desc", "id", "sub", "tags", "topic"]
    CRED_FIELD_NUMBER: _ClassVar[int]
    DEL_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    cred: _containers.RepeatedCompositeFieldContainer[ServerCred]
    desc: TopicDesc
    id: str
    sub: _containers.RepeatedCompositeFieldContainer[TopicSub]
    tags: _containers.RepeatedScalarFieldContainer[str]
    topic: str
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., desc: _Optional[_Union[TopicDesc, _Mapping]] = ..., sub: _Optional[_Iterable[_Union[TopicSub, _Mapping]]] = ..., tags: _Optional[_Iterable[str]] = ..., cred: _Optional[_Iterable[_Union[ServerCred, _Mapping]]] = ..., **kwargs) -> None: ...

class ServerMsg(_message.Message):
    __slots__ = ["ctrl", "data", "info", "meta", "pres", "topic"]
    CTRL_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    INFO_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    PRES_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    ctrl: ServerCtrl
    data: ServerData
    info: ServerInfo
    meta: ServerMeta
    pres: ServerPres
    topic: str
    def __init__(self, ctrl: _Optional[_Union[ServerCtrl, _Mapping]] = ..., data: _Optional[_Union[ServerData, _Mapping]] = ..., pres: _Optional[_Union[ServerPres, _Mapping]] = ..., meta: _Optional[_Union[ServerMeta, _Mapping]] = ..., info: _Optional[_Union[ServerInfo, _Mapping]] = ..., topic: _Optional[str] = ...) -> None: ...

class ServerPres(_message.Message):
    __slots__ = ["acs", "actor_user_id", "del_id", "del_seq", "seq_id", "src", "target_user_id", "topic", "user_agent", "what"]
    class What(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = []
    ACS: ServerPres.What
    ACS_FIELD_NUMBER: _ClassVar[int]
    ACTOR_USER_ID_FIELD_NUMBER: _ClassVar[int]
    DEL: ServerPres.What
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_SEQ_FIELD_NUMBER: _ClassVar[int]
    GONE: ServerPres.What
    MSG: ServerPres.What
    OFF: ServerPres.What
    ON: ServerPres.What
    READ: ServerPres.What
    RECV: ServerPres.What
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    SRC_FIELD_NUMBER: _ClassVar[int]
    TAGS: ServerPres.What
    TARGET_USER_ID_FIELD_NUMBER: _ClassVar[int]
    TERM: ServerPres.What
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    UA: ServerPres.What
    UPD: ServerPres.What
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    X3: ServerPres.What
    acs: AccessMode
    actor_user_id: str
    del_id: int
    del_seq: _containers.RepeatedCompositeFieldContainer[SeqRange]
    seq_id: int
    src: str
    target_user_id: str
    topic: str
    user_agent: str
    what: ServerPres.What
    def __init__(self, topic: _Optional[str] = ..., src: _Optional[str] = ..., what: _Optional[_Union[ServerPres.What, str]] = ..., user_agent: _Optional[str] = ..., seq_id: _Optional[int] = ..., del_id: _Optional[int] = ..., del_seq: _Optional[_Iterable[_Union[SeqRange, _Mapping]]] = ..., target_user_id: _Optional[str] = ..., actor_user_id: _Optional[str] = ..., acs: _Optional[_Union[AccessMode, _Mapping]] = ...) -> None: ...

class ServerResp(_message.Message):
    __slots__ = ["clmsg", "srvmsg", "status"]
    CLMSG_FIELD_NUMBER: _ClassVar[int]
    SRVMSG_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    clmsg: ClientMsg
    srvmsg: ServerMsg
    status: RespCode
    def __init__(self, status: _Optional[_Union[RespCode, str]] = ..., srvmsg: _Optional[_Union[ServerMsg, _Mapping]] = ..., clmsg: _Optional[_Union[ClientMsg, _Mapping]] = ...) -> None: ...

class Session(_message.Message):
    __slots__ = ["auth_level", "device_id", "language", "remote_addr", "session_id", "user_agent", "user_id"]
    AUTH_LEVEL_FIELD_NUMBER: _ClassVar[int]
    DEVICE_ID_FIELD_NUMBER: _ClassVar[int]
    LANGUAGE_FIELD_NUMBER: _ClassVar[int]
    REMOTE_ADDR_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    auth_level: AuthLevel
    device_id: str
    language: str
    remote_addr: str
    session_id: str
    user_agent: str
    user_id: str
    def __init__(self, session_id: _Optional[str] = ..., user_id: _Optional[str] = ..., auth_level: _Optional[_Union[AuthLevel, str]] = ..., remote_addr: _Optional[str] = ..., user_agent: _Optional[str] = ..., device_id: _Optional[str] = ..., language: _Optional[str] = ...) -> None: ...

class SetDesc(_message.Message):
    __slots__ = ["default_acs", "private", "public", "trusted"]
    DEFAULT_ACS_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    TRUSTED_FIELD_NUMBER: _ClassVar[int]
    default_acs: DefaultAcsMode
    private: bytes
    public: bytes
    trusted: bytes
    def __init__(self, default_acs: _Optional[_Union[DefaultAcsMode, _Mapping]] = ..., public: _Optional[bytes] = ..., private: _Optional[bytes] = ..., trusted: _Optional[bytes] = ...) -> None: ...

class SetQuery(_message.Message):
    __slots__ = ["cred", "desc", "sub", "tags"]
    CRED_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    cred: ClientCred
    desc: SetDesc
    sub: SetSub
    tags: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, desc: _Optional[_Union[SetDesc, _Mapping]] = ..., sub: _Optional[_Union[SetSub, _Mapping]] = ..., tags: _Optional[_Iterable[str]] = ..., cred: _Optional[_Union[ClientCred, _Mapping]] = ...) -> None: ...

class SetSub(_message.Message):
    __slots__ = ["mode", "user_id"]
    MODE_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    mode: str
    user_id: str
    def __init__(self, user_id: _Optional[str] = ..., mode: _Optional[str] = ...) -> None: ...

class SubscriptionEvent(_message.Message):
    __slots__ = ["action", "del_id", "mode", "private", "read_id", "recv_id", "topic", "user_id"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    READ_ID_FIELD_NUMBER: _ClassVar[int]
    RECV_ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    del_id: int
    mode: AccessMode
    private: bytes
    read_id: int
    recv_id: int
    topic: str
    user_id: str
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., topic: _Optional[str] = ..., user_id: _Optional[str] = ..., del_id: _Optional[int] = ..., read_id: _Optional[int] = ..., recv_id: _Optional[int] = ..., mode: _Optional[_Union[AccessMode, _Mapping]] = ..., private: _Optional[bytes] = ...) -> None: ...

class TopicDesc(_message.Message):
    __slots__ = ["acs", "created_at", "defacs", "del_id", "is_chan", "last_seen_time", "last_seen_user_agent", "online", "private", "public", "read_id", "recv_id", "seq_id", "state", "state_at", "touched_at", "trusted", "updated_at"]
    ACS_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    DEFACS_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    IS_CHAN_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_TIME_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    ONLINE_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    READ_ID_FIELD_NUMBER: _ClassVar[int]
    RECV_ID_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_AT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    TOUCHED_AT_FIELD_NUMBER: _ClassVar[int]
    TRUSTED_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    acs: AccessMode
    created_at: int
    defacs: DefaultAcsMode
    del_id: int
    is_chan: bool
    last_seen_time: int
    last_seen_user_agent: str
    online: bool
    private: bytes
    public: bytes
    read_id: int
    recv_id: int
    seq_id: int
    state: str
    state_at: int
    touched_at: int
    trusted: bytes
    updated_at: int
    def __init__(self, created_at: _Optional[int] = ..., updated_at: _Optional[int] = ..., touched_at: _Optional[int] = ..., defacs: _Optional[_Union[DefaultAcsMode, _Mapping]] = ..., acs: _Optional[_Union[AccessMode, _Mapping]] = ..., seq_id: _Optional[int] = ..., read_id: _Optional[int] = ..., recv_id: _Optional[int] = ..., del_id: _Optional[int] = ..., public: _Optional[bytes] = ..., private: _Optional[bytes] = ..., state: _Optional[str] = ..., state_at: _Optional[int] = ..., trusted: _Optional[bytes] = ..., is_chan: bool = ..., online: bool = ..., last_seen_time: _Optional[int] = ..., last_seen_user_agent: _Optional[str] = ...) -> None: ...

class TopicEvent(_message.Message):
    __slots__ = ["action", "desc", "name"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    desc: TopicDesc
    name: str
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., name: _Optional[str] = ..., desc: _Optional[_Union[TopicDesc, _Mapping]] = ...) -> None: ...

class TopicSub(_message.Message):
    __slots__ = ["acs", "del_id", "deleted_at", "last_seen_time", "last_seen_user_agent", "online", "private", "public", "read_id", "recv_id", "seq_id", "topic", "touched_at", "trusted", "updated_at", "user_id"]
    ACS_FIELD_NUMBER: _ClassVar[int]
    DELETED_AT_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_TIME_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    ONLINE_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    READ_ID_FIELD_NUMBER: _ClassVar[int]
    RECV_ID_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    TOUCHED_AT_FIELD_NUMBER: _ClassVar[int]
    TRUSTED_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    acs: AccessMode
    del_id: int
    deleted_at: int
    last_seen_time: int
    last_seen_user_agent: str
    online: bool
    private: bytes
    public: bytes
    read_id: int
    recv_id: int
    seq_id: int
    topic: str
    touched_at: int
    trusted: bytes
    updated_at: int
    user_id: str
    def __init__(self, updated_at: _Optional[int] = ..., deleted_at: _Optional[int] = ..., online: bool = ..., acs: _Optional[_Union[AccessMode, _Mapping]] = ..., read_id: _Optional[int] = ..., recv_id: _Optional[int] = ..., public: _Optional[bytes] = ..., trusted: _Optional[bytes] = ..., private: _Optional[bytes] = ..., user_id: _Optional[str] = ..., topic: _Optional[str] = ..., touched_at: _Optional[int] = ..., seq_id: _Optional[int] = ..., del_id: _Optional[int] = ..., last_seen_time: _Optional[int] = ..., last_seen_user_agent: _Optional[str] = ...) -> None: ...

class Unused(_message.Message):
    __slots__ = []
    def __init__(self) -> None: ...

class AuthLevel(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []

class InfoNote(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []

class CallEvent(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []

class RespCode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []

class Crud(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
