# Drafty: Rich Message Format

Drafty is a text format used by Tinode to style messages. The intent of Drafty is to be expressive just enough without opening too many possibilities for security issues. One may think of it as JSON-encapsulated [markdown](https://en.wikipedia.org/wiki/Markdown). Drafty is influenced by FB's [draft.js](https://draftjs.org/) specification. As of the time of this writing [Javascript](https://github.com/tinode/tinode-js/blob/master/src/drafty.js), [Java](https://github.com/tinode/tindroid/blob/master/tinodesdk/src/main/java/co/tinode/tinodesdk/model/Drafty.java) and [Swift](https://github.com/tinode/ios/blob/master/TinodeSDK/model/Drafty.swift) implementations exist. A [Go implementation](https://github.com/tinode/chat/blob/master/server/drafty/drafty.go) can convert Drafy to plain text and previews.

## Example

> this is **bold**, `code` and _italic_, ~~strike~~<br/>
>  combined **bold and _italic_**<br/>
>  an url: https://www.example.com/abc#fragment and another _[https://web.tinode.co](https://web.tinode.co)_<br/>
>  this is a [@mention](#) and a [#hashtag](#) in a string<br/>
> second [#hashtag](#)<br/>

Sample Drafty-JSON representation of the text above:
```js
{
   "txt":  "this is bold, code and italic, strike combined bold and italic an url: https://www.example.com/abc#fragment and another www.tinode.co this is a @mention and a #hashtag in a string second #hashtag",
   "fmt": [
       { "at":8, "len":4,"tp":"ST" },{ "at":14, "len":4, "tp":"CO" },{ "at":23, "len":6, "tp":"EM"},
       { "at":31, "len":6, "tp":"DL" },{ "tp":"BR", "len":1, "at":37 },{ "at":56, "len":6, "tp":"EM" },
       { "at":47, "len":15, "tp":"ST" },{ "tp":"BR", "len":1, "at":62 },{ "at":120, "len":13, "tp":"EM" },
       { "at":71, "len":36, "key":0 },{ "at":120, "len":13, "key":1 },{ "tp":"BR", "len":1, "at":133 },
       { "at":144, "len":8, "key":2 },{ "at":159, "len":8, "key":3 },{ "tp":"BR", "len":1, "at":179 },
       { "at":187, "len":8, "key":3 },{ "tp":"BR", "len":1, "at":195 }
   ],
   "ent": [
       { "tp":"LN", "data":{ "url":"https://www.example.com/abc#fragment" } },
       { "tp":"LN", "data":{ "url":"http://www.tinode.co" } },
       { "tp":"MN", "data":{ "val":"mention" } },
       { "tp":"HT", "data":{ "val":"hashtag" } }
   ]
}
```

## Structure

Drafty object has three fields: plain text `txt`, inline markup `fmt`, and entities `ent`.

### Plain Text

The message to be sent is converted to plain Unicode text with all markup stripped and kept in `txt` field. In general, a valid Drafy may contain just the `txt` field.

### Inline Formatting `fmt`

Inline formatting is an array of individual styles in the `fmt` field. Each style is represented by an object with at least `at` and `len` fields. The `at` value means 0-based offset into `txt`, `len` is the number of characters to apply formatting to. The third value of style is either `tp` or `key`.

If `tp` is provided, it means the style is a basic text decoration:
 * `BR`: line break.
 * `CO`: code or monotyped text, possibly with different background: `monotype`.
 * `DL`: deleted or strikethrough text: ~~strikethrough~~.
 * `EM`: emphasized text, usually represented as italic: _italic_.
 * `FM`: form / set of fields; may also be represented as an entity.
 * `HD`: hidden content.
 * `HL`: highlighted text, such as text in a different color or with a different background; the color cannot be specified.
 * `RW`: logical grouping of formats, a row; may also be represented as an entity.
 * `ST`: strong or bold text: **bold**.

If key is provided, it's a 0-based index into the `ent` array which contains extended style parameters such as an image or an URL:
 * `AU`: embedded audio.
 * `BN`: interactive button.
 * `EX`: generic attachment.
 * `FM`: form / set of fields; may also be represented as a basic decoration.
 * `HT`: hashtag, e.g. [#hashtag](#).
 * `IM`: inline image.
 * `LN`: link (URL) [https://api.tinode.co](https://api.tinode.co).
 * `MN`: mention such as [@tinode](#).
 * `RW`: logical grouping of formats, a row; may also be represented as a basic decoration.
 * `VC`: video (and audio) calls.
 * `VD`: inline video.

Examples:
 * `{ "at":8, "len":4, "tp":"ST"}`: apply formatting `ST` (strong/bold) to 4 characters starting at offset 8 into `txt`.
 * `{ "at":144, "len":8, "key":2 }`: insert entity `ent[2]` into position 144, the entity spans 8 characters.
 * `{ "at":-1, "len":0, "key":4 }`: show the `ent[4]` as a file attachment, don't apply any styling to text.

The clients should be able to handle missing `at`, `key`, and `len` values. Missing values are assumed to be equal to `0`.

The indexes `at` and `len` are measured in [Unicode code points](https://developer.mozilla.org/en-US/docs/Glossary/Code_point), not bytes or characters. The behavior of multi-codepoint glyphs such as emojis with Fitzpatrick skin tone modifiers, variation selectors, or grouped with `ZWJ` is currently undefined.

#### `FM`: a form, an ordered set or fields

Form provides means to add paragraph-level formatting to a logical group of elements. It may be represented as a text decoration or as an entity.
<table>
<tr><th>Do you agree?</th></tr>
<tr><td><a href="">Yes</a></td></tr>
<tr><td><a href="">No</a></td></tr>
</table>

```js
{
 "txt": "Do you agree? Yes No",
 "fmt": [
   {"len": 20, "tp": "FM"}, // missing 'at' is zero: "at": 0
   {"len": 13, "tp": "ST"}
   {"at": 13, "len": 1, "tp": "BR"},
   {"at": 14, "len": 3}, // missing 'key' is zero: "key": 0
   {"at": 17, "len": 1, "tp": "BR"},
   {"at": 18, "len": 2, "key": 1},
 ],
 "ent": [
   {"tp": "BN", "data": {"name": "yes", "act": "pub", "val": "oh yes!"}},
   {"tp": "BN", "data": {"name": "no", "act": "pub"}}
 ]
}
```
If a `Yes` button is pressed in the example above, the client is expected to send a message to the server with the following content:
```js
{
 "txt": "Yes",
 "fmt": [{
   "at":-1
 }],
 "ent": [{
   "tp": "EX",
   "data": {
     "mime": "text/x-drafty-fr", // drafty form-response.
     "val": {
       "seq": 15, // seq id of the message containing the form.
       "resp": {"yes": "oh yes!"}
     }
   }
 }]
}
```

The form may be optionally represented as an entity:
```js
{
  "tp": "FM",
  "data": {
    "su": true
  }
}
```
The `data.su` describes how interactive form elements behave after the click. An `"su": true` indicates that the form is `single use`: the form should change after the first interaction to show that it's no longer accepting input.

### Entities `ent`

In general, an entity is a text decoration which requires additional (possibly large) data. An entity is represented by an object with two fields: `tp` indicates type of the entity, `data` is type-dependent styling information. Unknown fields are ignored.

#### `AU`: embedded audio record
`AU` is an audio record. The `data` contains the following fields:
```js
{
  "tp": "AU",
  "data": {
    "mime": "audio/aac",
    "val": "Rt53jUU...iVBORw0KGgoA==",
    "ref": "/v0/file/s/e769gvt1ILE.m4v",
    "preview": "Aw4JKBkAAAAKMSM...vHxgcJhsgESAY"
    "duration": 180000,
    "name": "ding_dong.m4a",
    "size": 595496
  }
}
```
 * `mime`: data type, such as 'audio/ogg'.
 * `val`: optional in-band audio data: base64-encoded audio bits.
 * `ref`: optional reference to out-of-band audio data. Either `val` or `ref` must be present.
 * `preview`: base64-encoded array of bytes to generate a visual preview; each byte is an amplitude bar.
 * `duration`: duration of the record in milliseconds.
 * `name`: optional name of the original file.
 * `size`: optional size of the file in bytes.

To create a message with just a single audio record and no text, use the following Drafty:
```js
{
  txt: " ",
  fmt: [{len: 1}],
  ent: [{tp: "AU", data: {<your audio data here>}]}
}
```

_IMPORTANT Security Consideration_: the `val` and `ref` fields may contain malicious payload. The client should restrict URL scheme in the `ref` field to `http` and `https` only. The client should present content of `val` field to the user only if it's correctly converted to an audio.


#### `BN`: interactive button
`BN` offers an option to send data to a server, either origin server or another one. The `data` contains the following fields:
```js
{
  "tp": "BN",
  "data": {
    "name": "confirmation",
    "act": "url",
    "val": "some-value",
    "ref": "https://www.example.com/path/?foo=bar"
  }
}
```
* `act`: type of action in response to button click:
  * `pub`: send a Drafty-formatted `{pub}` message to the current topic with the form data as an attachment:
  ```js
  { "tp":"EX", "data":{ "mime":"text/x-drafty-fr", "val": { "seq": 3, "resp": { "confirmation": "some-value" } } } }
  ```
  * `url`: issue an `HTTP GET` request to the URL from the `data.ref` field. The following query parameters are appended to the URL: `<name>=<val>`, `uid=<current-user-ID>`, `topic=<topic name>`, `seq=<message sequence ID>`.
  * `note`: send a `{note}` message to the current topic with `what` set to `data` (not currently implemented, contact us if you need it).
  ```js
  { "what": "data", "data": { "mime": "text/x-drafty-fr", "val": { "seq": 3, "resp": { "confirmation": "some-value" } } }
  ```
* `name`: optional name of the button which is reported back to the server.
* `val`: additional opaque data.
* `ref`: the URL for the `url` action.

If the `name` is provided but `val` is not, `val` is assumed to be `1`. If `name` is undefined then nether `name` nor `val` are sent.

The button in the example above will send an HTTP GET to https://www.example.com/path/?foo=bar&confirmation=some-value&uid=usrFsk73jYRR&topic=grpnG99YhENiQU&seq=3 assuming the current user ID is `usrFsk73jYRR`, the topic is `grpnG99YhENiQU`, and the sequence ID of message with the button is `3`.

_IMPORTANT Security Consideration_: the client should restrict URL scheme in the `ref` field to `http` and `https` only.


#### `EX`: file attachment
`EX` is an attachment which the client should not try to interpret. The `data` contains the following fields:
```js
{
  "tp": "EX",
  "data": {
    "mime", "text/plain",
    "val", "Q3l0aG9uPT0w...PT00LjAuMAo=",
    "ref": "/v0/file/s/abcdef12345.txt",
    "name", "requirements.txt",
    "size": 1234
  }
}
```
* `mime`: data type, such as 'application/octet-stream'.
* `val`: optional in-band base64-encoded file data.
* `ref`: optional reference to out-of-band file data. Either `val` or `ref` must be present.
* `name`: optional name of the original file.
* `size`: optional size of the file in bytes.

To generate a message with the file attachment shown as a downloadable file, use the following format:
```js
{
  at: -1,
  len: 0,
  key: <EX entity reference>
}
```

_IMPORTANT Security Consideration_: the `ref` fields may contain malicious payload. The client should restrict URL scheme in the `ref` field to `http` and `https` only.


#### `IM`: inline image or attached image with inline preview
`IM` is an image. The `data` contains the following fields:
```js
{
  "tp": "IM",
  "data": {
    "mime": "image/png",
    "val": "Rt53jUU...iVBORw0KGgoA==",
    "ref": "/v0/file/s/abcdef12345.jpg",
    "width": 512,
    "height": 512,
    "name": "sample_image.png",
    "size": 123456
  }
}
```
 * `mime`: data type, such as 'image/jpeg'.
 * `val`: optional in-band image data: base64-encoded image bits.
 * `ref`: optional reference to out-of-band image data. Either `val` or `ref` must be present.
 * `width`, `height`: linear dimensions of the image in pixels.
 * `name`: optional name of the original file.
 * `size`: optional size of the file in bytes.

To create a message with just a single image and no text, use the following Drafty:
```js
{
  txt: " ",
  fmt: [{len: 1}],
  ent: [{tp: "IM", data: {<your image data here>}]}
}
```

_IMPORTANT Security Consideration_: the `val` and `ref` fields may contain malicious payload. The client should restrict URL scheme in the `ref` field to `http` and `https` only. The client should present content of `val` field to the user only if it's correctly converted to an image.


#### `LN`: link (URL)
`LN` is an URL. The `data` contains a single `url` field:
`{ "tp": "LN", "data": { "url": "https://www.example.com/abc#fragment" } }`
The `url` could be any valid URL that the client knows how to interpret, for instance it could be email or phone URL too: `email:alice@example.com` or `tel:+17025550001`.

_IMPORTANT Security Consideration_: the `url` field may be maliciously constructed. The client should disable certain URL schemes such as `javascript:` and `data:`.


#### `MN`: mention such as [@alice](#)
Mention `data` contains a single `val` field with ID of the mentioned user:
```js
{ "tp":"MN", "data":{ "val":"usrFsk73jYRR" } }
```


#### `HT`: hashtag, e.g. [#tinode](#)
Hashtag `data` contains a single `val` field with the hashtag value which the client software needs to interpret, for instance it could be a search term:
```js
{ "tp":"HT", "data":{ "val":"tinode" } }
```

#### `VC`: video call control message
Video call `data` contains current state of the call and its duration:
```js
{
  "tp": "VC",
  "data": {
    "duration": 10000,
    "state": "disconnected",
    "incoming": false,
    "aonly": true
  }
}
```

* `duration`: call duration in milliseconds.
* `state`: surrent call state; supported states:
	* `accepted`: a call is established (ongoing).
	* `busy`: a call cannot be established because the callee is already in another call.
	* `finished`: a previously establied call has successfully finished.
	* `disconnected`: the call is dropped, for example because of an error.
	* `missed`: the call is missed, i.e. the callee didn't pick up the phone.
	* `declined`: the call is declined, i.e. the callee hung up before picking up.
* `incoming`: true if the call is incoming, otherwise the call is outgoing.
* `aonly`: true if this is an audio-only call (no video).

The `VC` may also be represented as a format `"fmt": [{"len": 1, "tp": "VC"}]` with no entity. In such a case all call information is contained in the `head` fields of the enclosing message.

#### `VD`: video with preview
`VD` represents a video recording. The `data` contains the following fields:
```js
{
  "tp": "VD",
  "data": {
    "mime": "video/webm",
    "ref": "/v0/file/s/abcdef12345.webm",
    "preview": "AsTrsU...k86n00Ggo=="
    "preref": "/v0/file/s/abcdef54321.jpeg",
    "premime": "image/jpeg",
    "width": 640,
    "height": 360,
    "duration": 32000,
    "name": " bigbuckbunny.webm",
    "size": 1234567
  }
}
```
 * `mime`: data type of the video, such as 'video/webm'.
 * `val`: optional in-band video data: base64-encoded video bits, usually not present (null).
 * `ref`: optional reference to an out-of-band video data. Either `val` or `ref` must be present.
 * `preview`: optional base64-encoded screencapture image from the video (poster).
 * `preref`: optional reference to an out-of-band screencapture image from the video (poster).
 * `premime`: data type of the optional screencapture image (poster); assumed 'image/jpeg' if missing.
 * `width`, `height`: linear dimensions of the video and poster in pixels.
 * `duration`: duration of the video in milliseconds.
 * `name`: optional name of the original file.
 * `size`: optional size of the file in bytes.

To create a message with just a single video and no text, use the following Drafty:
```js
{
  txt: " ",
  fmt: [{len: 1}],
  ent: [{tp: "VD", data: {<your video data here>}]}
}
```

_IMPORTANT Security Consideration_: the `val`, `ref`, `preview` fields may contain malicious payload. The client should restrict URL scheme in the `ref` and `preview` fields to `http` and `https` only. The client should present content of `val` field to the user only if it's correctly converted to a video.
