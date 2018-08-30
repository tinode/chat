# Script for packaging generated model_pb2*.py into tinode_grpc module.

import setuptools
from subprocess import Popen, PIPE

with open("README.md", "r") as fh:
    long_description = fh.read()

def git_version():
    p = Popen(['git', 'describe', '--tags'], stdout=PIPE, stderr=PIPE)
    p.stderr.close()
    line = p.stdout.readlines()[0].decode("ascii").strip()
    if line.startswith("v"): #strip "v" prefix from git tag "v0.15.5" -> "0.15.5".
        line = line[1:]
    return line

setuptools.setup(
    name="tinode_grpc",
    version=git_version(),
    author="Tinode Authors",
    author_email="gene@tinode.co",
    description="Tinode gRPC bindings.",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/tinode/chat",
    packages=setuptools.find_packages(),
    install_requires=['grpcio>=1.9.1'],
    license="Apache 2.0",
    keywords="chat messaging messenger im tinode",
    classifiers=(
        "Programming Language :: Python :: 2",
        "Programming Language :: Python :: 2.7",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.6",
        "License :: OSI Approved :: Apache Software License",
        "Operating System :: OS Independent",
        "Topic :: Communications :: Chat",
        "Intended Audience :: Developers",
    ),
)
