.PHONY: build check test clean

build:
	$(MAKE) -C J-agent build

check:
	$(MAKE) -C J-agent check

test:
	$(MAKE) -C J-agent test

clean:
	$(MAKE) -C J-agent clean
