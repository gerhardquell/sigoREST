#!/usr/bin/env python3
"""
Setup script for sigoclient - Python client for sigoREST
"""

from setuptools import setup, find_packages

with open("README.md", "r", encoding="utf-8") as fh:
    long_description = fh.read()

setup(
    name="sigoclient",
    version="1.0.0",
    author="Gerhard Quell",
    author_email="gquell@skequell.de",
    description="Python client for sigoREST - OpenAI-compatible AI Gateway",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/gquell/sigoREST",
    packages=find_packages(),
    classifiers=[
        "Development Status :: 4 - Beta",
        "Intended Audience :: Developers",
        "Topic :: Software Development :: Libraries :: Python Modules",
        "License :: OSI Approved :: MIT License",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.7",
        "Programming Language :: Python :: 3.8",
        "Programming Language :: Python :: 3.9",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
    ],
    python_requires=">=3.7",
    install_requires=[
        "requests>=2.25.0",
    ],
    extras_require={
        "dev": [
            "pytest>=6.0",
            "black",
            "flake8",
        ],
    },
)