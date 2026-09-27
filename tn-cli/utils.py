"""Utility functions for tn-cli."""

from __future__ import print_function

import base64
import json
from PIL import Image
try:
    from io import BytesIO as memory_io
except ImportError:
    from cStringIO import StringIO as memory_io
import mimetypes
import os

from tn_globals import stdoutln

# Maximum in-band (included directly into the message) attachment size which fits into
# a message of 256K in size, assuming base64 encoding and 1024 bytes of overhead.
# This is size of an object *before* base64 encoding is applied.
MAX_INBAND_ATTACHMENT_SIZE = 195840

# Absolute maximum attachment size to be used with the server = 8MB.
MAX_EXTERN_ATTACHMENT_SIZE = 1 << 23

# Maximum allowed linear dimension of an inline image in pixels.
MAX_IMAGE_DIM = 768

# String used as a delete marker. I.e. when a value needs to be deleted, use this string
DELETE_MARKER = 'DEL!'

# Unicode DEL character used internally by Tinode when a value needs to be deleted.
TINODE_DEL = '␡'


# Python is retarded.
class dotdict(dict):
    """dot.notation access to dictionary attributes"""
    __getattr__ = dict.get
    __setattr__ = dict.__setitem__
    __delattr__ = dict.__delitem__


# Pack name, description, and avatar into a theCard.
def makeTheCard(fn, note, photofile):
    card = None

    if (fn != None and fn.strip() != "") or photofile != None or note != None:
        card = {}
        if fn != None:
            fn = fn.strip()
            card['fn'] = TINODE_DEL if fn == DELETE_MARKER or fn == '' else fn

        if note != None:
            note = note.strip()
            card['note'] = TINODE_DEL if note == DELETE_MARKER or note == '' else note

        if photofile != None:
            if photofile == '' or photofile == DELETE_MARKER:
                # Delete the avatar.
                card['photo'] = {
                    'data': TINODE_DEL
                }
            else:
                try:
                    f = open(photofile, 'rb')
                    # File extension is used as a file type
                    mimetype = mimetypes.guess_type(photofile)
                    if mimetype[0]:
                        mimetype = mimetype[0].split("/")[1]
                    else:
                        mimetype = 'jpeg'
                    data = base64.b64encode(f.read())
                    # python3 fix.
                    if type(data) is not str:
                        data = data.decode()
                    card['photo'] = {
                        'data': data,
                        'type': mimetype
                    }
                    f.close()
                except IOError as err:
                    stdoutln("Error opening '" + photofile + "':", err)

    return card


# Create drafty representation of a message with an inline image.
def inline_image(filename):
    try:
        im = Image.open(filename, 'r')
        width = im.width
        height = im.height
        format = im.format if im.format else "JPEG"
        if width > MAX_IMAGE_DIM or height > MAX_IMAGE_DIM:
            # Scale the image
            scale = min(min(width, MAX_IMAGE_DIM) / width, min(height, MAX_IMAGE_DIM) / height)
            width = int(width * scale)
            height = int(height * scale)
            resized = im.resize((width, height))
            im.close()
            im = resized

        mimetype = 'image/' + format.lower()
        bitbuffer = memory_io()
        im.save(bitbuffer, format=format)
        data = base64.b64encode(bitbuffer.getvalue())

        # python3 fix.
        if type(data) is not str:
            data = data.decode()

        result = {
            'txt': ' ',
            'fmt': [{'len': 1}],
            'ent': [{'tp': 'IM', 'data':
                {'val': data, 'mime': mimetype, 'width': width, 'height': height,
                    'name': os.path.basename(filename)}}]
        }
        im.close()
        return result
    except IOError as err:
        stdoutln("Failed processing image '" + filename + "':", err)
        return None


# Create a drafty message with an *in-band* attachment.
def attachment(filename):
    try:
        f = open(filename, 'rb')
        # Try to guess the mime type.
        mimetype = mimetypes.guess_type(filename)[0]
        data = base64.b64encode(f.read())
        # python3 fix.
        if type(data) is not str:
            data = data.decode()
        result = {
            'fmt': [{'at': -1}],
            'ent': [{'tp': 'EX', 'data':{
                'val': data, 'mime': mimetype, 'name':os.path.basename(filename)
            }}]
        }
        f.close()
        return result
    except IOError as err:
        stdoutln("Error processing attachment '" + filename + "':", err)
        return None


# encode_to_bytes converts the 'src' to a byte array.
# An object/dictionary is first converted to json string then it's converted to bytes.
# A string is directly converted to bytes.
def encode_to_bytes(src):
    if src == None:
        return None
    if isinstance(src, str):
        return ('"' + src + '"').encode()
    return json.dumps(src).encode('utf-8')


# Parse credentials
def parse_cred(cred):
    result = None
    if cred != None:
        result = []
        for c in cred.split(","):
            parts = c.split(":")
            from tinode_grpc import pb
            result.append(pb.ClientCred(method=parts[0] if len(parts) > 0 else None,
                value=parts[1] if len(parts) > 1 else None,
                response=parts[2] if len(parts) > 2 else None))

    return result


# Parse trusted values: [staff,rm-verified].
def parse_trusted(trusted):
    result = None
    if trusted != None:
        result = {}
        for t in trusted.split(","):
            t = t.strip()
            if t.startswith("rm-"):
                result[t[3:]] = False
            else:
                result[t] = True

    return result
