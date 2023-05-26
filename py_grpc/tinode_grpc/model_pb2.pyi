from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AuthLevel(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    NONE: _ClassVar[AuthLevel]
    ANON: _ClassVar[AuthLevel]
    AUTH: _ClassVar[AuthLevel]
    ROOT: _ClassVar[AuthLevel]

class InfoNote(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    X1: _ClassVar[InfoNote]
    READ: _ClassVar[InfoNote]
    RECV: _ClassVar[InfoNote]
    KP: _ClassVar[InfoNote]
    CALL: _ClassVar[InfoNote]

class CallEvent(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    X2: _ClassVar[CallEvent]
    ACCEPT: _ClassVar[CallEvent]
    ANSWER: _ClassVar[CallEvent]
    HANG_UP: _ClassVar[CallEvent]
    ICE_CANDIDATE: _ClassVar[CallEvent]
    INVITE: _ClassVar[CallEvent]
    OFFER: _ClassVar[CallEvent]
    RINGING: _ClassVar[CallEvent]

class RespCode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    CONTINUE: _ClassVar[RespCode]
    DROP: _ClassVar[RespCode]
    RESPOND: _ClassVar[RespCode]
    REPLACE: _ClassVar[RespCode]

class Crud(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    CREATE: _ClassVar[Crud]
    UPDATE: _ClassVar[Crud]
    DELETE: _ClassVar[Crud]
NONE: AuthLevel
ANON: AuthLevel
AUTH: AuthLevel
ROOT: AuthLevel
X1: InfoNote
READ: InfoNote
RECV: InfoNote
KP: InfoNote
CALL: InfoNote
X2: CallEvent
ACCEPT: CallEvent
ANSWER: CallEvent
HANG_UP: CallEvent
ICE_CANDIDATE: CallEvent
INVITE: CallEvent
OFFER: CallEvent
RINGING: CallEvent
CONTINUE: RespCode
DROP: RespCode
RESPOND: RespCode
REPLACE: RespCode
CREATE: Crud
UPDATE: Crud
DELETE: Crud

class Unused(_message.Message):
    __slots__ = []
    def __init__(self) -> None: ...

class DefaultAcsMode(_message.Message):
    __slots__ = ["auth", "anon"]
    AUTH_FIELD_NUMBER: _ClassVar[int]
    ANON_FIELD_NUMBER: _ClassVar[int]
    auth: str
    anon: str
    def __init__(self, auth: _Optional[str] = ..., anon: _Optional[str] = ...) -> None: ...

class AccessMode(_message.Message):
    __slots__ = ["want", "given"]
    WANT_FIELD_NUMBER: _ClassVar[int]
    GIVEN_FIELD_NUMBER: _ClassVar[int]
    want: str
    given: str
    def __init__(self, want: _Optional[str] = ..., given: _Optional[str] = ...) -> None: ...

class SetSub(_message.Message):
    __slots__ = ["user_id", "mode"]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    user_id: str
    mode: str
    def __init__(self, user_id: _Optional[str] = ..., mode: _Optional[str] = ...) -> None: ...

class ClientCred(_message.Message):
    __slots__ = ["method", "value", "response", "params"]
    class ParamsEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    METHOD_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    method: str
    value: str
    response: str
    params: _containers.ScalarMap[str, bytes]
    def __init__(self, method: _Optional[str] = ..., value: _Optional[str] = ..., response: _Optional[str] = ..., params: _Optional[_Mapping[str, bytes]] = ...) -> None: ...

class SetDesc(_message.Message):
    __slots__ = ["default_acs", "public", "private", "trusted"]
    DEFAULT_ACS_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    TRUSTED_FIELD_NUMBER: _ClassVar[int]
    default_acs: DefaultAcsMode
    public: bytes
    private: bytes
    trusted: bytes
    def __init__(self, default_acs: _Optional[_Union[DefaultAcsMode, _Mapping]] = ..., public: _Optional[bytes] = ..., private: _Optional[bytes] = ..., trusted: _Optional[bytes] = ...) -> None: ...

class GetOpts(_message.Message):
    __slots__ = ["if_modified_since", "user", "topic", "since_id", "before_id", "limit"]
    IF_MODIFIED_SINCE_FIELD_NUMBER: _ClassVar[int]
    USER_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    SINCE_ID_FIELD_NUMBER: _ClassVar[int]
    BEFORE_ID_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    if_modified_since: int
    user: str
    topic: str
    since_id: int
    before_id: int
    limit: int
    def __init__(self, if_modified_since: _Optional[int] = ..., user: _Optional[str] = ..., topic: _Optional[str] = ..., since_id: _Optional[int] = ..., before_id: _Optional[int] = ..., limit: _Optional[int] = ...) -> None: ...

class GetQuery(_message.Message):
    __slots__ = ["what", "desc", "sub", "data"]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    what: str
    desc: GetOpts
    sub: GetOpts
    data: GetOpts
    def __init__(self, what: _Optional[str] = ..., desc: _Optional[_Union[GetOpts, _Mapping]] = ..., sub: _Optional[_Union[GetOpts, _Mapping]] = ..., data: _Optional[_Union[GetOpts, _Mapping]] = ...) -> None: ...

class SetQuery(_message.Message):
    __slots__ = ["desc", "sub", "tags", "cred"]
    DESC_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    CRED_FIELD_NUMBER: _ClassVar[int]
    desc: SetDesc
    sub: SetSub
    tags: _containers.RepeatedScalarFieldContainer[str]
    cred: ClientCred
    def __init__(self, desc: _Optional[_Union[SetDesc, _Mapping]] = ..., sub: _Optional[_Union[SetSub, _Mapping]] = ..., tags: _Optional[_Iterable[str]] = ..., cred: _Optional[_Union[ClientCred, _Mapping]] = ...) -> None: ...

class SeqRange(_message.Message):
    __slots__ = ["low", "hi"]
    LOW_FIELD_NUMBER: _ClassVar[int]
    HI_FIELD_NUMBER: _ClassVar[int]
    low: int
    hi: int
    def __init__(self, low: _Optional[int] = ..., hi: _Optional[int] = ...) -> None: ...

class ClientHi(_message.Message):
    __slots__ = ["id", "user_agent", "ver", "device_id", "lang", "platform", "background"]
    ID_FIELD_NUMBER: _ClassVar[int]
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    VER_FIELD_NUMBER: _ClassVar[int]
    DEVICE_ID_FIELD_NUMBER: _ClassVar[int]
    LANG_FIELD_NUMBER: _ClassVar[int]
    PLATFORM_FIELD_NUMBER: _ClassVar[int]
    BACKGROUND_FIELD_NUMBER: _ClassVar[int]
    id: str
    user_agent: str
    ver: str
    device_id: str
    lang: str
    platform: str
    background: bool
    def __init__(self, id: _Optional[str] = ..., user_agent: _Optional[str] = ..., ver: _Optional[str] = ..., device_id: _Optional[str] = ..., lang: _Optional[str] = ..., platform: _Optional[str] = ..., background: bool = ...) -> None: ...

class ClientAcc(_message.Message):
    __slots__ = ["id", "user_id", "scheme", "secret", "login", "tags", "desc", "cred", "token", "state", "auth_level", "tmp_scheme", "tmp_secret"]
    ID_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    SCHEME_FIELD_NUMBER: _ClassVar[int]
    SECRET_FIELD_NUMBER: _ClassVar[int]
    LOGIN_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    CRED_FIELD_NUMBER: _ClassVar[int]
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    AUTH_LEVEL_FIELD_NUMBER: _ClassVar[int]
    TMP_SCHEME_FIELD_NUMBER: _ClassVar[int]
    TMP_SECRET_FIELD_NUMBER: _ClassVar[int]
    id: str
    user_id: str
    scheme: str
    secret: bytes
    login: bool
    tags: _containers.RepeatedScalarFieldContainer[str]
    desc: SetDesc
    cred: _containers.RepeatedCompositeFieldContainer[ClientCred]
    token: bytes
    state: str
    auth_level: AuthLevel
    tmp_scheme: str
    tmp_secret: bytes
    def __init__(self, id: _Optional[str] = ..., user_id: _Optional[str] = ..., scheme: _Optional[str] = ..., secret: _Optional[bytes] = ..., login: bool = ..., tags: _Optional[_Iterable[str]] = ..., desc: _Optional[_Union[SetDesc, _Mapping]] = ..., cred: _Optional[_Iterable[_Union[ClientCred, _Mapping]]] = ..., token: _Optional[bytes] = ..., state: _Optional[str] = ..., auth_level: _Optional[_Union[AuthLevel, str]] = ..., tmp_scheme: _Optional[str] = ..., tmp_secret: _Optional[bytes] = ...) -> None: ...

class ClientLogin(_message.Message):
    __slots__ = ["id", "scheme", "secret", "cred"]
    ID_FIELD_NUMBER: _ClassVar[int]
    SCHEME_FIELD_NUMBER: _ClassVar[int]
    SECRET_FIELD_NUMBER: _ClassVar[int]
    CRED_FIELD_NUMBER: _ClassVar[int]
    id: str
    scheme: str
    secret: bytes
    cred: _containers.RepeatedCompositeFieldContainer[ClientCred]
    def __init__(self, id: _Optional[str] = ..., scheme: _Optional[str] = ..., secret: _Optional[bytes] = ..., cred: _Optional[_Iterable[_Union[ClientCred, _Mapping]]] = ...) -> None: ...

class ClientSub(_message.Message):
    __slots__ = ["id", "topic", "set_query", "get_query"]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    SET_QUERY_FIELD_NUMBER: _ClassVar[int]
    GET_QUERY_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    set_query: SetQuery
    get_query: GetQuery
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., set_query: _Optional[_Union[SetQuery, _Mapping]] = ..., get_query: _Optional[_Union[GetQuery, _Mapping]] = ...) -> None: ...

class ClientLeave(_message.Message):
    __slots__ = ["id", "topic", "unsub"]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    UNSUB_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    unsub: bool
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., unsub: bool = ...) -> None: ...

class ClientPub(_message.Message):
    __slots__ = ["id", "topic", "no_echo", "head", "content"]
    class HeadEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    NO_ECHO_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    no_echo: bool
    head: _containers.ScalarMap[str, bytes]
    content: bytes
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., no_echo: bool = ..., head: _Optional[_Mapping[str, bytes]] = ..., content: _Optional[bytes] = ...) -> None: ...

class ClientGet(_message.Message):
    __slots__ = ["id", "topic", "query"]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    query: GetQuery
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., query: _Optional[_Union[GetQuery, _Mapping]] = ...) -> None: ...

class ClientSet(_message.Message):
    __slots__ = ["id", "topic", "query"]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    query: SetQuery
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., query: _Optional[_Union[SetQuery, _Mapping]] = ...) -> None: ...

class ClientDel(_message.Message):
    __slots__ = ["id", "topic", "what", "del_seq", "user_id", "cred", "hard"]
    class What(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = []
        X0: _ClassVar[ClientDel.What]
        MSG: _ClassVar[ClientDel.What]
        TOPIC: _ClassVar[ClientDel.What]
        SUB: _ClassVar[ClientDel.What]
        USER: _ClassVar[ClientDel.What]
        CRED: _ClassVar[ClientDel.What]
    X0: ClientDel.What
    MSG: ClientDel.What
    TOPIC: ClientDel.What
    SUB: ClientDel.What
    USER: ClientDel.What
    CRED: ClientDel.What
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    DEL_SEQ_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    CRED_FIELD_NUMBER: _ClassVar[int]
    HARD_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    what: ClientDel.What
    del_seq: _containers.RepeatedCompositeFieldContainer[SeqRange]
    user_id: str
    cred: ClientCred
    hard: bool
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., what: _Optional[_Union[ClientDel.What, str]] = ..., del_seq: _Optional[_Iterable[_Union[SeqRange, _Mapping]]] = ..., user_id: _Optional[str] = ..., cred: _Optional[_Union[ClientCred, _Mapping]] = ..., hard: bool = ...) -> None: ...

class ClientNote(_message.Message):
    __slots__ = ["topic", "what", "seq_id", "unread", "event", "payload"]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    UNREAD_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_FIELD_NUMBER: _ClassVar[int]
    topic: str
    what: InfoNote
    seq_id: int
    unread: int
    event: CallEvent
    payload: bytes
    def __init__(self, topic: _Optional[str] = ..., what: _Optional[_Union[InfoNote, str]] = ..., seq_id: _Optional[int] = ..., unread: _Optional[int] = ..., event: _Optional[_Union[CallEvent, str]] = ..., payload: _Optional[bytes] = ...) -> None: ...

class ClientExtra(_message.Message):
    __slots__ = ["attachments", "on_behalf_of", "auth_level"]
    ATTACHMENTS_FIELD_NUMBER: _ClassVar[int]
    ON_BEHALF_OF_FIELD_NUMBER: _ClassVar[int]
    AUTH_LEVEL_FIELD_NUMBER: _ClassVar[int]
    attachments: _containers.RepeatedScalarFieldContainer[str]
    on_behalf_of: str
    auth_level: AuthLevel
    def __init__(self, attachments: _Optional[_Iterable[str]] = ..., on_behalf_of: _Optional[str] = ..., auth_level: _Optional[_Union[AuthLevel, str]] = ...) -> None: ...

class ClientMsg(_message.Message):
    __slots__ = ["hi", "acc", "login", "sub", "leave", "pub", "get", "set", "note", "extra"]
    HI_FIELD_NUMBER: _ClassVar[int]
    ACC_FIELD_NUMBER: _ClassVar[int]
    LOGIN_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    LEAVE_FIELD_NUMBER: _ClassVar[int]
    PUB_FIELD_NUMBER: _ClassVar[int]
    GET_FIELD_NUMBER: _ClassVar[int]
    SET_FIELD_NUMBER: _ClassVar[int]
    DEL_FIELD_NUMBER: _ClassVar[int]
    NOTE_FIELD_NUMBER: _ClassVar[int]
    EXTRA_FIELD_NUMBER: _ClassVar[int]
    hi: ClientHi
    acc: ClientAcc
    login: ClientLogin
    sub: ClientSub
    leave: ClientLeave
    pub: ClientPub
    get: ClientGet
    set: ClientSet
    note: ClientNote
    extra: ClientExtra
    def __init__(self, hi: _Optional[_Union[ClientHi, _Mapping]] = ..., acc: _Optional[_Union[ClientAcc, _Mapping]] = ..., login: _Optional[_Union[ClientLogin, _Mapping]] = ..., sub: _Optional[_Union[ClientSub, _Mapping]] = ..., leave: _Optional[_Union[ClientLeave, _Mapping]] = ..., pub: _Optional[_Union[ClientPub, _Mapping]] = ..., get: _Optional[_Union[ClientGet, _Mapping]] = ..., set: _Optional[_Union[ClientSet, _Mapping]] = ..., note: _Optional[_Union[ClientNote, _Mapping]] = ..., extra: _Optional[_Union[ClientExtra, _Mapping]] = ..., **kwargs) -> None: ...

class ServerCred(_message.Message):
    __slots__ = ["method", "value", "done"]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    DONE_FIELD_NUMBER: _ClassVar[int]
    method: str
    value: str
    done: bool
    def __init__(self, method: _Optional[str] = ..., value: _Optional[str] = ..., done: bool = ...) -> None: ...

class TopicDesc(_message.Message):
    __slots__ = ["created_at", "updated_at", "touched_at", "defacs", "acs", "seq_id", "read_id", "recv_id", "del_id", "public", "private", "state", "state_at", "trusted", "is_chan", "online", "last_seen_time", "last_seen_user_agent"]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    TOUCHED_AT_FIELD_NUMBER: _ClassVar[int]
    DEFACS_FIELD_NUMBER: _ClassVar[int]
    ACS_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    READ_ID_FIELD_NUMBER: _ClassVar[int]
    RECV_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    STATE_AT_FIELD_NUMBER: _ClassVar[int]
    TRUSTED_FIELD_NUMBER: _ClassVar[int]
    IS_CHAN_FIELD_NUMBER: _ClassVar[int]
    ONLINE_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_TIME_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    created_at: int
    updated_at: int
    touched_at: int
    defacs: DefaultAcsMode
    acs: AccessMode
    seq_id: int
    read_id: int
    recv_id: int
    del_id: int
    public: bytes
    private: bytes
    state: str
    state_at: int
    trusted: bytes
    is_chan: bool
    online: bool
    last_seen_time: int
    last_seen_user_agent: str
    def __init__(self, created_at: _Optional[int] = ..., updated_at: _Optional[int] = ..., touched_at: _Optional[int] = ..., defacs: _Optional[_Union[DefaultAcsMode, _Mapping]] = ..., acs: _Optional[_Union[AccessMode, _Mapping]] = ..., seq_id: _Optional[int] = ..., read_id: _Optional[int] = ..., recv_id: _Optional[int] = ..., del_id: _Optional[int] = ..., public: _Optional[bytes] = ..., private: _Optional[bytes] = ..., state: _Optional[str] = ..., state_at: _Optional[int] = ..., trusted: _Optional[bytes] = ..., is_chan: bool = ..., online: bool = ..., last_seen_time: _Optional[int] = ..., last_seen_user_agent: _Optional[str] = ...) -> None: ...

class TopicSub(_message.Message):
    __slots__ = ["updated_at", "deleted_at", "online", "acs", "read_id", "recv_id", "public", "trusted", "private", "user_id", "topic", "touched_at", "seq_id", "del_id", "last_seen_time", "last_seen_user_agent"]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    DELETED_AT_FIELD_NUMBER: _ClassVar[int]
    ONLINE_FIELD_NUMBER: _ClassVar[int]
    ACS_FIELD_NUMBER: _ClassVar[int]
    READ_ID_FIELD_NUMBER: _ClassVar[int]
    RECV_ID_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    TRUSTED_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    TOUCHED_AT_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_TIME_FIELD_NUMBER: _ClassVar[int]
    LAST_SEEN_USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    updated_at: int
    deleted_at: int
    online: bool
    acs: AccessMode
    read_id: int
    recv_id: int
    public: bytes
    trusted: bytes
    private: bytes
    user_id: str
    topic: str
    touched_at: int
    seq_id: int
    del_id: int
    last_seen_time: int
    last_seen_user_agent: str
    def __init__(self, updated_at: _Optional[int] = ..., deleted_at: _Optional[int] = ..., online: bool = ..., acs: _Optional[_Union[AccessMode, _Mapping]] = ..., read_id: _Optional[int] = ..., recv_id: _Optional[int] = ..., public: _Optional[bytes] = ..., trusted: _Optional[bytes] = ..., private: _Optional[bytes] = ..., user_id: _Optional[str] = ..., topic: _Optional[str] = ..., touched_at: _Optional[int] = ..., seq_id: _Optional[int] = ..., del_id: _Optional[int] = ..., last_seen_time: _Optional[int] = ..., last_seen_user_agent: _Optional[str] = ...) -> None: ...

class DelValues(_message.Message):
    __slots__ = ["del_id", "del_seq"]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_SEQ_FIELD_NUMBER: _ClassVar[int]
    del_id: int
    del_seq: _containers.RepeatedCompositeFieldContainer[SeqRange]
    def __init__(self, del_id: _Optional[int] = ..., del_seq: _Optional[_Iterable[_Union[SeqRange, _Mapping]]] = ...) -> None: ...

class ServerCtrl(_message.Message):
    __slots__ = ["id", "topic", "code", "text", "params"]
    class ParamsEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    CODE_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    code: int
    text: str
    params: _containers.ScalarMap[str, bytes]
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., code: _Optional[int] = ..., text: _Optional[str] = ..., params: _Optional[_Mapping[str, bytes]] = ...) -> None: ...

class ServerData(_message.Message):
    __slots__ = ["topic", "from_user_id", "timestamp", "deleted_at", "seq_id", "head", "content"]
    class HeadEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    FROM_USER_ID_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    DELETED_AT_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    topic: str
    from_user_id: str
    timestamp: int
    deleted_at: int
    seq_id: int
    head: _containers.ScalarMap[str, bytes]
    content: bytes
    def __init__(self, topic: _Optional[str] = ..., from_user_id: _Optional[str] = ..., timestamp: _Optional[int] = ..., deleted_at: _Optional[int] = ..., seq_id: _Optional[int] = ..., head: _Optional[_Mapping[str, bytes]] = ..., content: _Optional[bytes] = ...) -> None: ...

class ServerPres(_message.Message):
    __slots__ = ["topic", "src", "what", "user_agent", "seq_id", "del_id", "del_seq", "target_user_id", "actor_user_id", "acs"]
    class What(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = []
        X3: _ClassVar[ServerPres.What]
        ON: _ClassVar[ServerPres.What]
        OFF: _ClassVar[ServerPres.What]
        UA: _ClassVar[ServerPres.What]
        UPD: _ClassVar[ServerPres.What]
        GONE: _ClassVar[ServerPres.What]
        ACS: _ClassVar[ServerPres.What]
        TERM: _ClassVar[ServerPres.What]
        MSG: _ClassVar[ServerPres.What]
        READ: _ClassVar[ServerPres.What]
        RECV: _ClassVar[ServerPres.What]
        DEL: _ClassVar[ServerPres.What]
        TAGS: _ClassVar[ServerPres.What]
    X3: ServerPres.What
    ON: ServerPres.What
    OFF: ServerPres.What
    UA: ServerPres.What
    UPD: ServerPres.What
    GONE: ServerPres.What
    ACS: ServerPres.What
    TERM: ServerPres.What
    MSG: ServerPres.What
    READ: ServerPres.What
    RECV: ServerPres.What
    DEL: ServerPres.What
    TAGS: ServerPres.What
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    SRC_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_SEQ_FIELD_NUMBER: _ClassVar[int]
    TARGET_USER_ID_FIELD_NUMBER: _ClassVar[int]
    ACTOR_USER_ID_FIELD_NUMBER: _ClassVar[int]
    ACS_FIELD_NUMBER: _ClassVar[int]
    topic: str
    src: str
    what: ServerPres.What
    user_agent: str
    seq_id: int
    del_id: int
    del_seq: _containers.RepeatedCompositeFieldContainer[SeqRange]
    target_user_id: str
    actor_user_id: str
    acs: AccessMode
    def __init__(self, topic: _Optional[str] = ..., src: _Optional[str] = ..., what: _Optional[_Union[ServerPres.What, str]] = ..., user_agent: _Optional[str] = ..., seq_id: _Optional[int] = ..., del_id: _Optional[int] = ..., del_seq: _Optional[_Iterable[_Union[SeqRange, _Mapping]]] = ..., target_user_id: _Optional[str] = ..., actor_user_id: _Optional[str] = ..., acs: _Optional[_Union[AccessMode, _Mapping]] = ...) -> None: ...

class ServerMeta(_message.Message):
    __slots__ = ["id", "topic", "desc", "sub", "tags", "cred"]
    ID_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    SUB_FIELD_NUMBER: _ClassVar[int]
    DEL_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    CRED_FIELD_NUMBER: _ClassVar[int]
    id: str
    topic: str
    desc: TopicDesc
    sub: _containers.RepeatedCompositeFieldContainer[TopicSub]
    tags: _containers.RepeatedScalarFieldContainer[str]
    cred: _containers.RepeatedCompositeFieldContainer[ServerCred]
    def __init__(self, id: _Optional[str] = ..., topic: _Optional[str] = ..., desc: _Optional[_Union[TopicDesc, _Mapping]] = ..., sub: _Optional[_Iterable[_Union[TopicSub, _Mapping]]] = ..., tags: _Optional[_Iterable[str]] = ..., cred: _Optional[_Iterable[_Union[ServerCred, _Mapping]]] = ..., **kwargs) -> None: ...

class ServerInfo(_message.Message):
    __slots__ = ["topic", "from_user_id", "what", "seq_id", "src", "event", "payload"]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    FROM_USER_ID_FIELD_NUMBER: _ClassVar[int]
    WHAT_FIELD_NUMBER: _ClassVar[int]
    SEQ_ID_FIELD_NUMBER: _ClassVar[int]
    SRC_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_FIELD_NUMBER: _ClassVar[int]
    topic: str
    from_user_id: str
    what: InfoNote
    seq_id: int
    src: str
    event: CallEvent
    payload: bytes
    def __init__(self, topic: _Optional[str] = ..., from_user_id: _Optional[str] = ..., what: _Optional[_Union[InfoNote, str]] = ..., seq_id: _Optional[int] = ..., src: _Optional[str] = ..., event: _Optional[_Union[CallEvent, str]] = ..., payload: _Optional[bytes] = ...) -> None: ...

class ServerMsg(_message.Message):
    __slots__ = ["ctrl", "data", "pres", "meta", "info", "topic"]
    CTRL_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    PRES_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    INFO_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    ctrl: ServerCtrl
    data: ServerData
    pres: ServerPres
    meta: ServerMeta
    info: ServerInfo
    topic: str
    def __init__(self, ctrl: _Optional[_Union[ServerCtrl, _Mapping]] = ..., data: _Optional[_Union[ServerData, _Mapping]] = ..., pres: _Optional[_Union[ServerPres, _Mapping]] = ..., meta: _Optional[_Union[ServerMeta, _Mapping]] = ..., info: _Optional[_Union[ServerInfo, _Mapping]] = ..., topic: _Optional[str] = ...) -> None: ...

class ServerResp(_message.Message):
    __slots__ = ["status", "srvmsg", "clmsg"]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    SRVMSG_FIELD_NUMBER: _ClassVar[int]
    CLMSG_FIELD_NUMBER: _ClassVar[int]
    status: RespCode
    srvmsg: ServerMsg
    clmsg: ClientMsg
    def __init__(self, status: _Optional[_Union[RespCode, str]] = ..., srvmsg: _Optional[_Union[ServerMsg, _Mapping]] = ..., clmsg: _Optional[_Union[ClientMsg, _Mapping]] = ...) -> None: ...

class Session(_message.Message):
    __slots__ = ["session_id", "user_id", "auth_level", "remote_addr", "user_agent", "device_id", "language"]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    AUTH_LEVEL_FIELD_NUMBER: _ClassVar[int]
    REMOTE_ADDR_FIELD_NUMBER: _ClassVar[int]
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    DEVICE_ID_FIELD_NUMBER: _ClassVar[int]
    LANGUAGE_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    user_id: str
    auth_level: AuthLevel
    remote_addr: str
    user_agent: str
    device_id: str
    language: str
    def __init__(self, session_id: _Optional[str] = ..., user_id: _Optional[str] = ..., auth_level: _Optional[_Union[AuthLevel, str]] = ..., remote_addr: _Optional[str] = ..., user_agent: _Optional[str] = ..., device_id: _Optional[str] = ..., language: _Optional[str] = ...) -> None: ...

class ClientReq(_message.Message):
    __slots__ = ["msg", "sess"]
    MSG_FIELD_NUMBER: _ClassVar[int]
    SESS_FIELD_NUMBER: _ClassVar[int]
    msg: ClientMsg
    sess: Session
    def __init__(self, msg: _Optional[_Union[ClientMsg, _Mapping]] = ..., sess: _Optional[_Union[Session, _Mapping]] = ...) -> None: ...

class SearchQuery(_message.Message):
    __slots__ = ["user_id", "query"]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    user_id: str
    query: str
    def __init__(self, user_id: _Optional[str] = ..., query: _Optional[str] = ...) -> None: ...

class SearchFound(_message.Message):
    __slots__ = ["status", "query", "result"]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    RESULT_FIELD_NUMBER: _ClassVar[int]
    status: RespCode
    query: str
    result: _containers.RepeatedCompositeFieldContainer[TopicSub]
    def __init__(self, status: _Optional[_Union[RespCode, str]] = ..., query: _Optional[str] = ..., result: _Optional[_Iterable[_Union[TopicSub, _Mapping]]] = ...) -> None: ...

class TopicEvent(_message.Message):
    __slots__ = ["action", "name", "desc"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESC_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    name: str
    desc: TopicDesc
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., name: _Optional[str] = ..., desc: _Optional[_Union[TopicDesc, _Mapping]] = ...) -> None: ...

class AccountEvent(_message.Message):
    __slots__ = ["action", "user_id", "default_acs", "public", "tags"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_ACS_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    user_id: str
    default_acs: DefaultAcsMode
    public: bytes
    tags: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., user_id: _Optional[str] = ..., default_acs: _Optional[_Union[DefaultAcsMode, _Mapping]] = ..., public: _Optional[bytes] = ..., tags: _Optional[_Iterable[str]] = ...) -> None: ...

class SubscriptionEvent(_message.Message):
    __slots__ = ["action", "topic", "user_id", "del_id", "read_id", "recv_id", "mode", "private"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    DEL_ID_FIELD_NUMBER: _ClassVar[int]
    READ_ID_FIELD_NUMBER: _ClassVar[int]
    RECV_ID_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    topic: str
    user_id: str
    del_id: int
    read_id: int
    recv_id: int
    mode: AccessMode
    private: bytes
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., topic: _Optional[str] = ..., user_id: _Optional[str] = ..., del_id: _Optional[int] = ..., read_id: _Optional[int] = ..., recv_id: _Optional[int] = ..., mode: _Optional[_Union[AccessMode, _Mapping]] = ..., private: _Optional[bytes] = ...) -> None: ...

class MessageEvent(_message.Message):
    __slots__ = ["action", "msg"]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    MSG_FIELD_NUMBER: _ClassVar[int]
    action: Crud
    msg: ServerData
    def __init__(self, action: _Optional[_Union[Crud, str]] = ..., msg: _Optional[_Union[ServerData, _Mapping]] = ...) -> None: ...
