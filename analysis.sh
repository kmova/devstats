#!/bin/sh
#ruby analysis.rb old '' jsons/*.json | tee analysis/old.txt
#ruby analysis.rb old_repository 'repository' jsons/*.json | tee analysis/old_repository.txt
#ruby analysis.rb old_payload 'payload' jsons/*.json | tee analysis/old_payload.txt
ruby analysis.rb old_payload_pages 'payload,pages,i:0' jsons/*.json | tee analysis/old_payload_pages.txt