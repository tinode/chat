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

Drafty object has three fields: plain text `txt`, inline markup `fmt`, entities `ent`.

### Plain Text

The message to be sent is converted to plain Unicode text with all markup stripped and kept in `txt` field. In general, a valid Drafy may contain just the `txt` field.

### Inline Formatting

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
 * `EX`: file attachment

Examples:
 * `{ "at":8, "len":4, "tp":"ST"}`: apply formatting `ST` (strong/bold) to 4 characters starting at offset 8 into `txt`.
 * `{ "at":144, "len":8, "key":2 }`: insert entity `ent[2]` into position 144, the entity spans 8 characters.
 * `{ "at":-1, "len":0, "key":4 }`: show the `ent[4]` as a file attachment, don't apply any styling to text.

### Entities

In general, an entity is a text decoration which requires additional (possibly large) data. An entity is represented by an object with two fields: `tp` indicates type of the entity, `data` is type-dependent styling information. Unknown fields are ignored.

#### `LN`: link (URL)
`LN` is an URL. The `data` contains a single `url` field:
`{ "tp": "LN", "data": { "url": "https://www.example.com/abc#fragment" } }`
The `url` could be any valid URl that the client knows how to interpret, for instance it could be email or phone URL too: `email:alice@example.com` or `tel:+17025550001`.

#### `IM`: inline image
`IM` is an image. The `data` contains the following fields:
```js
{
  "tp": "IM",
  "data": {
    "mime": "image/png",
    "val": "Rt53jUU...iVBORw0KGgoA==",
    "width": 512,
    "height": 512,
    "name": "sample_image.png"
  }
}
```
 * `mime`: data type, such as 'image/jpeg'.
 * `val`: base64-encoded image bits.
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
    "name", "requirements.txt"
  }
}
```
* `mime`: data type, such as 'application/octet-stream'.
* `val`: base64-encoded file data.
* `name`: optional name of the original file.
* `size`: optional size of the file in bytes.

To generate a message with the file attachment shown as a downloadable file, use the following format: `{ at: -1, len: 0, key: <EX entity reference> }`.

#### `MN`: mention such as [@alice](#)
Mention `data` contains a single `val` field with ID of the mentioned user:
`{ "tp":"MN", "data":{ "val":"usrFsk73jYRR" } }`

#### `HT`: hashtag, e.g. [#tinode](#)
Hashtag `data` contains a single `val` field with the hashtag value which the client software needs to interpret, for instance it could be a search term:
`{ "tp":"HT", "data":{ "val":"tinode" } }`
