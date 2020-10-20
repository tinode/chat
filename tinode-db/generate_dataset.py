# Script that generates a synthetic Tinode dataset in the same format as data.json.
# Run as:
# $ python generate_dataset.py --num_users X
#
# User ids and passwords are: userN and userN123 where N is in [0..X).
# The output will be dumped to stdout.

import argparse as ap
import json
import numpy as np
import random
import sys

parser = ap.ArgumentParser(description='Simple python script to generate synthetic Tinode datasets.')
parser.add_argument('--num_users', type=int, default=50, help='Number of user accounts (default: 50).')
args = parser.parse_args()

random.seed()
np.random.seed()

data = dict()

# Users.
num_users = args.num_users
users = list()
ul = ['user%d' % i for i in range(num_users)]

for i in range(num_users):
  userid = ul[i]
  u = {
    'createdAt': '-%dh' % random.randint(1, 300),
    'email': '%s@example.com' % userid,
    'passhash': '%s123' % userid,
    'private': {'comment': 'some comment 123'},
    'public': {'fn': userid},
    'tags': [userid],
    'state': 'ok',
    'status': {
      'text': 'my status %s' % userid
    },
    'username': userid,
  }
  users.append(u)

data['users'] = users

# Groups.
group_topics = list()
# TODO: make it configurable.
num_groups = random.randint(1, num_users >> 1)
for i in range(num_groups):
  owner = random.choice(ul)
  g = { 
    'createdAt': '-%dh' % random.randint(1, 300),
    'name': '*ABCgroup%d' % i,
    'owner': owner,
    'tags': ['group%d' % i],
    'public': {'fn': 'My group %d' % i}
  }
  group_topics.append(g)

data['grouptopics'] = group_topics

# P2P subs.
p2p_subs = list()
ids = set()

# TODO: Poisson mean should be configurable.
num_contacts = np.random.poisson(40, num_users).tolist()
num_contacts = [min(max(2, int(s)), num_users - 1) for s in num_contacts]

all_ids = [i for i in range(num_users)]

for i in range(num_users):
  contacts = [ul[x] for x in random.sample(all_ids, num_contacts[i]) if x > i]
  for c in contacts:
    p2p = { 
      'createdAt': '-%dh' % random.randint(1, 300),
      'users': [{'name': ul[i]}, {'name': c}]
    }
    p2p_subs.append(p2p)

data['p2psubs'] = p2p_subs

# Group subs.
# TODO: Poisson mean should be configurable as well.
sizes = np.random.poisson(4, num_groups).tolist()
sizes = [min(max(2, int(s)), num_users) for s in sizes]
group_subs = list()

for i in range(num_groups): 
  k = sizes[i]
  gs = random.sample(ul, k) 
  for uid in gs:
    g = {
      'createdAt': '-%dh' % random.randint(1, 300),
      'topic': '*ABCgroup%d' % i,
      'user': uid
    }
    group_subs.append(g)

data['groupsubs'] = group_subs

json.dump(data, sys.stdout, ensure_ascii=False, indent=4)
