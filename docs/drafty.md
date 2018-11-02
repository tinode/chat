# Drafty: Rich Message Format

Drafty is a text format used by Tinode to style messages. The intent of Drafty is to be expressive just enough without opening too many possibilities for security issues. Drafty is influenced by FB's [draft.js](https://draftjs.org/) specification. As of the time of this writing [Javascript](/tinode/example-react-js/blob/master/drafty.js) and [Java](/tinode/android-example/blob/master/tinodesdk/src/main/java/co/tinode/tinodesdk/model/Drafty.java) implementations exist.

## Example

> this is **bold**, `code` and _italic_, ~~strike~~<br/>
>  combined **bold and _italic_**<br/>
>  an url: https://www.example.com/abc#fragment and another _[https://api.tinode.co](https://api.tinode.co)_<br/>
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
 * `ST`: strong or bold text: **bold**.
 * `EM`: emphasized text, usually represented as italic: _italic_.
 * `DL`: deleted or strikethrough text: ~~strikethrough~~.
 * `CO`: code or monotyped text, possibly with different background: `monotype`.
 * `BR`: line break.

If key is provided, it's a 0-based index into the `ent` field which contains an entity definition such as an image or an URL:
 * `LN`: link (URL) [https://api.tinode.co](https://api.tinode.co)
 * `MN`: mention such as [@tinode](#)
 * `HT`: hashtag, e.g. [#hashtag](#)
 * `IM`: inline image
 * `EX`: generic attachment
 * `FM`: form / set of fields
 * `BN`: interactive button

Examples:
 * `{ "at":8, "len":4, "tp":"ST"}`: apply formatting `ST` (strong/bold) to 4 characters starting at offset 8 into `txt`.
 * `{ "at":144, "len":8, "key":2 }`: insert entity `ent[2]` into position 144, the entity spans 8 characters.
 * `{ "at":-1, "len":0, "key":4 }`: show the `ent[4]` as a file attachment, don't apply any styling to text.

### Entities `ent`

In general, an entity is a text decoration which requires additional (possibly large) data. An entity is represented by an object with two fields: `tp` indicates type of the entity, `data` is type-dependent styling information. Unknown fields are ignored.

#### `LN`: link (URL)
`LN` is an URL. The `data` contains a single `url` field:
`{ "tp": "LN", "data": { "url": "https://www.example.com/abc#fragment" } }`
The `url` could be any valid URl that the client knows how to interpret, for instance it could be email or phone URL too: `email:alice@example.com` or `tel:+17025550001`.

#### `IM`: inline image or attached image with inline preview
`IM` is an image. The `data` contains the following fields:
```js
{
  "tp": "IM",
  "data": {
    "mime": "image/png",
    "val": "Rt53jUU...iVBORw0KGgoA==",
    "ref": "https://api.tinode.co/file/s/abcdef12345.jpg",
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
 * `width`, `height`: linear dimensions of the image in pixels
 * `name`: optional name of the original file.
 * `size`: optional size of the file in bytes

To create a message with just a single image and no text, use the following Drafty:
```js
{
  txt: " ",
  fmt: {at: 0, len: 1, key: 0},
  ent: {tp: "IM", data: {<your image data here>}}
}
```

#### `EX`: file attachment
`EX` is an attachment which the client should not try to interpret. The `data` contains the following fields:
```js
{
  "tp": "EX",
  "data": {
    "mime", "text/plain",
    "val", "Q3l0aG9uPT0w...PT00LjAuMAo=",
    "ref": "https://api.tinode.co/file/s/abcdef12345.txt",
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

#### `FM`: a form, an ordered set or fields
Form provides means to arrange an array of text or Drafty elements in a predictable order:
<table>
<tr><th>Do you agree?</th></tr>
<tr><td>[Yes](#)</span></td></tr>
<tr><td>[No](#)</span></td></tr>
</table>

```js
{
  "tp": "FM",
  "data": {
    "name": "consent",
    "layout": "vlist",
    "val": [
      {"txt": "Do you agree?", "fmt": {"len": 12, "tp": "ST"},
      {"txt": "Yes", "fmt": {"len":3}, "ent":{"tp": "BN", "data": {"name": "yes", "act": "pub"}}},
      {"txt": "No", "fmt": {"len":2}, "ent":{"tp": "BN", "data": {"name": "no", "act": "pub"}}}
    ]
  }
}
```
* `name`: optional name of the form.
* `layout`: optional name of layout:
  * `vlist`: elements are arranged in a column, default order.
  * `hlist`: elements are arranged in a row.
* `val`: array of elements. Either plain text or Drafty object including other forms. Implementations may impose limits on the maximum number of elements in the list and the depth of nesting.

#### `BN`: interactive button
`BN` offers an option to send data to a server, either origin server or another one. The `data` contains the following fields:
```js
{
  "tp": "BN",
  "data": {
    "name": "confirmation",
    "act": "url",
    "val": "some-value",
    "ref": "https://www.example.com/"
  }
}
```
* `act`: type of action in response to button click:
  * `pub`: send a `{pub}` message to the current server.
  * `url`: issue an `HTTP GET` request to the URL from the `data.ref` field. A `name=val` and `uid=<current-user-ID>` query parameters are appended to the url.
* `name`: optional name of the button which is reported back to the server.
* `val`: additional opaque data. If `name` is provided but `val` is not, `val` is assumed to be `1`.
* `ref`: the actual URL for the `url` action.

The button in this example will send a HTTP GET to https://www.example.com/?confirmation=some-value&uid=usrFsk73jYRR
