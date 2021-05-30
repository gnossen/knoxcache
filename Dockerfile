FROM phusion/baseimage:18.04-1.0.0

COPY pagecacher /pagecacher

EXPOSE 8080

CMD /pagecacher
