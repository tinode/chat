# theCard: Person/Topic Description Format

Tinode uses `theCard` to store and transmit descriptions of people and topics.

_The format used to be called `vCard`, the same as [vCard](https://www.rfc-editor.org/rfc/rfc6350.txt) while being incompatible. That caused some confusion and prompted renaming the format to `theCard`. There are references to `vcard` and `vCard` troughout the code and documentation. It should be assumed that in all those cases it's meant to be `theCard` unless explicitly stated otherwise._

When `JSON` is used to represent `theCard` data, it does it differently than [jCard](https://tools.ietf.org/html/rfc7095). `theCard` and `jCard` are incompatible. The main difference is that `theCard` uses objects to represent logically related data while `jCard` uses ordered arrays.

`theCard` is structured as an object:

```js
{
  fn: "John Doe", // string, formatted name of the person or topic.
  photo: { // object, avatar photo; either 'data' or 'ref' must be present, all other fields are optional.
    type: "jpeg", // string, MIME type but with 'image/' dropped.
    data: "Rt53jUU...iVBORw0KGgoA==", // string, base64-encoded binary image data
    ref: "https://api.tinode.co/file/s/abcdef12345.jpg", // string, URL of the image.
    width: 512, // integer, image width in pixels.
    height: 512, // integer, image height in pixels.
    size: 123456 // integer, image size in bytes.
  },
  note: "Some notes", // string, description of a person or a topic.
  //
  // None of the following fields are implemented by any known client:
  //
  n: { // object, person's structured name.
    surname: "Miner", // surname or last or family name.
    given: "Coal", // first or given name.
    additional: "Diamond", // additional name, such as middle name or patronymic.
    prefix: "Dr.", // prefix, such as honorary title or gender designation.
    suffix: "Jr.", // suffix, such as 'Jr' or 'II'.
  },
  org: { // object, organization the person or topic belongs to.
    fn: "Most Evil Corp", // string, formatted name of the organisation.
    title: "CEO", // string, person's job title at the organisation.
  },
  tel: [ // array of objects, list of phone numbers associated with the person or topic.
    {
      type: "HOME", // string, contact designation.
      uri: "tel:+17025551234" // string, phone number as URI.
    }, ...
  ],
  email: [ // array of objects, list of person's email addresses
    {
      type: "WORK", // string, optional designation
      uri: "email:alice@example.com", // string, email address
    }, ...
  ],
  comm: [ // array of objects, list of person's other contact/communication options.
    {
      type: "WORK",
      name: "Tinode",
      uri: "tn:usrRkDVe0PYDOo", // string, URI specific to the contact type.
    },
    {
      type: "OTHER",
      name: "Website",
      uri: "https://tinode.co", // string, URI specific to the contact type.
    }, ...
  ],
  bday: { // object, person's birthday.
    y: 1970, // integer, year
    m: 1, // integer, month 1..12
    d: 15 // integer, day 1..31
  },
}
```

All fields are optional. Tinode clients currently use only `fn` and `photo` fields. If other fields are needed in the future,
then they will be adopted from the correspondent [vCard](https://www.rfc-editor.org/rfc/rfc6350.txt) fields.
