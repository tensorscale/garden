#!/bin/bash

migrate -path ./migrations -database sqlite3://garden.sqlite3 up
